package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ledger/internal/money"
	"ledger/internal/store"
)

const (
	recognizeLimit  = 3 << 20
	recognizeMaxImg = 2 << 20
	recognizeRecent = 40
	deepSeekModel   = "deepseek-flash"
	deepSeekDefault = "https://api.deepseek.com"
)

type recognizeIn struct {
	Text  string `json:"text"`
	Image string `json:"image"`
}

type recognizeOut struct {
	Kind       string `json:"kind"`
	Amount     int64  `json:"amount"`
	CategoryID int64  `json:"category_id"`
	Date       string `json:"date"`
	Note       string `json:"note"`
	Shared     bool   `json:"shared"`
}

type modelDraft struct {
	Kind         string          `json:"kind"`
	Amount       json.RawMessage `json:"amount"`
	CategoryID   int64           `json:"category_id"`
	CategoryName string          `json:"category_name"`
	Date         string          `json:"date"`
	Note         string          `json:"note"`
	Shared       bool            `json:"shared"`
}

func (s *Server) recognize(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.cfg.DeepSeekKey == "" {
		writeErr(w, http.StatusServiceUnavailable, "未配置识别服务")
		return
	}
	var in recognizeIn
	if !decodeBodyLimit(w, r, &in, recognizeLimit) {
		return
	}
	text := strings.TrimSpace(in.Text)
	image, err := normalizeImage(in.Image)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if text == "" && image == "" {
		writeErr(w, http.StatusBadRequest, "请提供账单文字或图片")
		return
	}

	categories, err := s.store.Categories()
	if err != nil {
		s.fail(w, err)
		return
	}
	recent, err := s.store.RecentTransactions(recognizeRecent)
	if err != nil {
		s.fail(w, err)
		return
	}

	draft, err := s.callDeepSeek(r.Context(), text, image, categories, recent)
	if err != nil {
		log.Printf("ledger: recognize: %v", err)
		writeErr(w, http.StatusBadGateway, "识别失败，请稍后重试或改为手记")
		return
	}
	out, err := bindDraft(draft, categories)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) callDeepSeek(ctx context.Context, text, image string, categories []store.Category, recent []store.Transaction) (modelDraft, error) {
	userContent := any(buildUserText(text, time.Now()))
	if image != "" {
		parts := []map[string]any{
			{"type": "text", "text": buildUserText(text, time.Now())},
			{"type": "image_url", "image_url": map[string]string{"url": image, "detail": "high"}},
		}
		userContent = parts
	}
	payload, err := json.Marshal(map[string]any{
		"model": deepSeekModel,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt(categories, recent)},
			{"role": "user", "content": userContent},
		},
		"response_format": map[string]string{"type": "json_object"},
		"thinking":        map[string]string{"type": "disabled"},
		"max_tokens":      400,
	})
	if err != nil {
		return modelDraft{}, err
	}

	base := strings.TrimRight(s.cfg.DeepSeekURL, "/")
	if base == "" {
		base = deepSeekDefault
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return modelDraft{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.DeepSeekKey)

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return modelDraft{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return modelDraft{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return modelDraft{}, fmt.Errorf("deepseek status %d", resp.StatusCode)
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return modelDraft{}, err
	}
	if len(envelope.Choices) == 0 {
		return modelDraft{}, fmt.Errorf("empty deepseek response")
	}
	var draft modelDraft
	if err := json.Unmarshal([]byte(extractJSON(envelope.Choices[0].Message.Content)), &draft); err != nil {
		return modelDraft{}, fmt.Errorf("parse model json: %w", err)
	}
	return draft, nil
}

func systemPrompt(categories []store.Category, recent []store.Transaction) string {
	var b strings.Builder
	b.WriteString("你是家庭账本的记账助手。根据账单文字或图片，对照已有分类和近期账目，整理成一笔记账。")
	b.WriteString("只输出一个 JSON 对象，字段为 kind, amount, category_id, category_name, date, note, shared。")
	b.WriteString("kind 只能是 expense 或 income。amount 是人民币元，最多两位小数。")
	b.WriteString("category_id 必须是下列分类之一，命名和分类习惯尽量接近近期账目。")
	b.WriteString("date 为 YYYY-MM-DD。note 简短，模仿近期备注的写法，没有备注就空字符串。")
	b.WriteString("shared：全家一起的为 true，个人的为 false。看不出就 false。\n分类：\n")
	for _, category := range categories {
		if category.Archived {
			continue
		}
		kind := "支出"
		if category.Kind == store.KindIncome {
			kind = "收入"
		}
		fmt.Fprintf(&b, "%d %s %s\n", category.ID, kind, category.Name)
	}
	b.WriteString("近期账目：\n")
	if len(recent) == 0 {
		b.WriteString("（暂无）\n")
		return b.String()
	}
	for _, tx := range recent {
		kind := "支出"
		if tx.Kind == store.KindIncome {
			kind = "收入"
		}
		share := " 个人"
		if tx.Shared {
			share = " 共同"
		}
		fmt.Fprintf(&b, "%s %s %.2f %s %s%s\n", tx.Date, kind, float64(tx.Amount)/100, tx.CategoryName, tx.Note, share)
	}
	return b.String()
}

func buildUserText(text string, now time.Time) string {
	if text == "" {
		return "请阅读图片中的账单，今天是 " + now.Format("2006-01-02") + "。"
	}
	return "今天是 " + now.Format("2006-01-02") + "。账单内容：\n" + text
}

func bindDraft(draft modelDraft, categories []store.Category) (recognizeOut, error) {
	if err := checkKindJSON(draft.Kind); err != nil {
		return recognizeOut{}, err
	}
	cents, err := parseModelAmount(draft.Amount)
	if err != nil {
		return recognizeOut{}, err
	}
	date := strings.TrimSpace(draft.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return recognizeOut{}, fmt.Errorf("识别出的日期无效")
	}
	id := matchCategory(draft, categories)
	if id == 0 {
		return recognizeOut{}, fmt.Errorf("无法对应到现有分类")
	}
	note := strings.TrimSpace(draft.Note)
	if utf8.RuneCountInString(note) > 100 {
		note = string([]rune(note)[:100])
	}
	return recognizeOut{
		Kind:       draft.Kind,
		Amount:     cents,
		CategoryID: id,
		Date:       date,
		Note:       note,
		Shared:     draft.Shared,
	}, nil
}

func matchCategory(draft modelDraft, categories []store.Category) int64 {
	for _, category := range categories {
		if category.ID == draft.CategoryID && category.Kind == draft.Kind && !category.Archived {
			return category.ID
		}
	}
	name := strings.TrimSpace(draft.CategoryName)
	if name == "" {
		return 0
	}
	for _, category := range categories {
		if category.Kind == draft.Kind && !category.Archived && category.Name == name {
			return category.ID
		}
	}
	return 0
}

func parseModelAmount(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0, fmt.Errorf("没有识别到金额")
	}
	if strings.HasPrefix(text, `"`) {
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err != nil {
			return 0, fmt.Errorf("没有识别到金额")
		}
		text = quoted
	} else {
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("没有识别到金额")
		}
		text = strconv.FormatFloat(n, 'f', -1, 64)
	}
	cents, err := money.ParseYuan(text)
	if err != nil {
		return 0, err
	}
	return cents, nil
}

func checkKindJSON(kind string) error {
	if kind == store.KindExpense || kind == store.KindIncome {
		return nil
	}
	return fmt.Errorf("无法判断是支出还是收入")
}

func normalizeImage(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "data:image/") {
		return "", fmt.Errorf("图片格式不正确")
	}
	media, data, ok := strings.Cut(raw, ";base64,")
	if !ok {
		return "", fmt.Errorf("图片格式不正确")
	}
	switch media {
	case "data:image/jpeg", "data:image/jpg", "data:image/png", "data:image/webp", "data:image/gif":
	default:
		return "", fmt.Errorf("仅支持 JPEG、PNG、WebP 或 GIF")
	}
	// 4/3 is the base64 expansion; reject obviously oversized payloads.
	if len(data)*3/4 > recognizeMaxImg {
		return "", fmt.Errorf("图片不能超过 2 MB")
	}
	if media == "data:image/jpg" {
		return "data:image/jpeg;base64," + data, nil
	}
	return raw, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}
