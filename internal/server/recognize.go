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
	recognizeMaxTx  = 20
)

type recognizeIn struct {
	Text  string `json:"text"`
	Image string `json:"image"`
}

type recognizeOut struct {
	Entries []recognizeEntry `json:"entries"`
}

type recognizeEntry struct {
	Kind       string `json:"kind"`
	Amount     int64  `json:"amount"`
	CategoryID int64  `json:"category_id"`
	ActivityID int64  `json:"activity_id"`
	CardID     int64  `json:"card_id"`
	Date       string `json:"date"`
	Note       string `json:"note"`
	Shared     bool   `json:"shared"`
}

type modelDraft struct {
	Kind         string          `json:"kind"`
	Amount       json.RawMessage `json:"amount"`
	CategoryID   int64           `json:"category_id"`
	CategoryName string          `json:"category_name"`
	ActivityID   int64           `json:"activity_id"`
	ActivityName string          `json:"activity_name"`
	CardID       int64           `json:"card_id"`
	CardName     string          `json:"card_name"`
	CardLast4    string          `json:"card_last4"`
	Date         string          `json:"date"`
	Note         string          `json:"note"`
	Shared       bool            `json:"shared"`
}

func (s *Server) recognize(w http.ResponseWriter, r *http.Request, sess session) {
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

	categories, err := sess.Book.Categories()
	if err != nil {
		s.fail(w, err)
		return
	}
	recent, err := sess.Book.RecentTransactions(recognizeRecent)
	if err != nil {
		s.fail(w, err)
		return
	}
	activities, err := sess.Book.Activities()
	if err != nil {
		s.fail(w, err)
		return
	}
	cards, err := sess.Book.Cards()
	if err != nil {
		s.fail(w, err)
		return
	}

	drafts, err := s.callDeepSeek(r.Context(), text, image, categories, activities, cards, recent)
	if err != nil {
		log.Printf("ledger: recognize: %v", err)
		writeErr(w, http.StatusBadGateway, "识别失败，请稍后重试或改为手记")
		return
	}
	out, err := bindDrafts(drafts, categories, activities, cards)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) callDeepSeek(ctx context.Context, text, image string, categories []store.Category, activities []store.Activity, cards []store.Card, recent []store.Transaction) ([]modelDraft, error) {
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
			{"role": "system", "content": systemPrompt(categories, activities, cards, recent)},
			{"role": "user", "content": userContent},
		},
		"response_format": map[string]string{"type": "json_object"},
		"thinking":        map[string]string{"type": "disabled"},
		"max_tokens":      1600,
	})
	if err != nil {
		return nil, err
	}

	base := strings.TrimRight(s.cfg.DeepSeekURL, "/")
	if base == "" {
		base = deepSeekDefault
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.cfg.DeepSeekKey)

	client := &http.Client{Timeout: 75 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("deepseek status %d", resp.StatusCode)
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("empty deepseek response")
	}
	drafts, err := parseModelJSON(envelope.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("parse model json: %w", err)
	}
	return drafts, nil
}

const recognizeInstructions = `根据文字或图片整理家庭账本。

一条对应一笔独立订单或一次独立付款。同一付款里的多件商品不要拆开；不同订单或不同付款不要合并。

只输出 {"entries":[{kind,amount,category_id,category_name,activity_id,activity_name,card_id,card_name,date,note,shared}]}。
kind 为 expense 或 income。amount 为人民币元，最多两位小数。
category_id、activity_id 必须是下列编号，每笔单独选最合适的一个；活动看不出则选默认。
card_id 能对应到卡则填编号，看不出则 0。
date 为 YYYY-MM-DD。note 简短，只写这一笔，并模仿近期备注；没有则空字符串。
shared 在全家一起时为 true，个人或看不出时为 false。
`

func systemPrompt(categories []store.Category, activities []store.Activity, cards []store.Card, recent []store.Transaction) string {
	var b strings.Builder
	b.WriteString(recognizeInstructions)
	b.WriteString("\n分类\n")
	for _, category := range categories {
		if category.Archived {
			continue
		}
		fmt.Fprintf(&b, "%d %s %s\n", category.ID, kindLabel(category.Kind), category.Name)
	}
	b.WriteString("\n活动\n")
	if len(activities) == 0 {
		b.WriteString("（暂无）\n")
	} else {
		for _, activity := range activities {
			fmt.Fprintf(&b, "%d %s\n", activity.ID, activityLine(activity))
		}
	}
	b.WriteString("\n银行卡\n")
	live := 0
	for _, card := range cards {
		if card.Archived {
			continue
		}
		live++
		fmt.Fprintf(&b, "%d %s %s\n", card.ID, cardKindLabel(card.Kind), cardHint(card.Bank, card.Name, card.Last4))
	}
	if live == 0 {
		b.WriteString("（暂无）\n")
	}
	b.WriteString("\n近期\n")
	if len(recent) == 0 {
		b.WriteString("（暂无）\n")
		return b.String()
	}
	for _, tx := range recent {
		b.WriteString(recentLine(tx))
		b.WriteByte('\n')
	}
	return b.String()
}

func activityLine(activity store.Activity) string {
	parts := []string{activity.Name}
	if activity.IsDefault {
		parts = append(parts, "默认")
	}
	if start := strings.TrimSpace(activity.StartDate); start != "" && !activity.IsDefault {
		parts = append(parts, start)
	}
	return strings.Join(parts, " ")
}

func recentLine(tx store.Transaction) string {
	parts := []string{
		tx.Date,
		kindLabel(tx.Kind),
		fmt.Sprintf("%.2f", float64(tx.Amount)/100),
		tx.CategoryName,
	}
	if name := strings.TrimSpace(tx.ActivityName); name != "" {
		parts = append(parts, name)
	}
	if card := cardHint(tx.CardBank, tx.CardName, tx.CardLast4); card != "" {
		parts = append(parts, card)
	}
	if note := strings.TrimSpace(tx.Note); note != "" {
		parts = append(parts, note)
	}
	if tx.Shared {
		parts = append(parts, "共同")
	} else {
		parts = append(parts, "个人")
	}
	return strings.Join(parts, " ")
}

func cardHint(bank, name, last4 string) string {
	parts := []string{}
	if bank = strings.TrimSpace(bank); bank != "" {
		parts = append(parts, bank)
	}
	if name = strings.TrimSpace(name); name != "" {
		parts = append(parts, name)
	}
	if last4 = strings.TrimSpace(last4); last4 != "" {
		parts = append(parts, last4)
	}
	return strings.Join(parts, " ")
}

func cardKindLabel(kind string) string {
	if kind == store.CardCredit {
		return "信用"
	}
	return "储蓄"
}

func kindLabel(kind string) string {
	if kind == store.KindIncome {
		return "收入"
	}
	return "支出"
}

func buildUserText(text string, now time.Time) string {
	head := "今天是 " + now.Format("2006-01-02") + "。"
	if text == "" {
		return head
	}
	return head + "\n" + text
}

func parseModelJSON(raw string) ([]modelDraft, error) {
	text := extractJSON(raw)
	var wrap struct {
		Entries []modelDraft `json:"entries"`
	}
	if err := json.Unmarshal([]byte(text), &wrap); err == nil && len(wrap.Entries) > 0 {
		return capDrafts(wrap.Entries), nil
	}
	var alt struct {
		Items        []modelDraft `json:"items"`
		Orders       []modelDraft `json:"orders"`
		Transactions []modelDraft `json:"transactions"`
	}
	if err := json.Unmarshal([]byte(text), &alt); err == nil {
		switch {
		case len(alt.Items) > 0:
			return capDrafts(alt.Items), nil
		case len(alt.Orders) > 0:
			return capDrafts(alt.Orders), nil
		case len(alt.Transactions) > 0:
			return capDrafts(alt.Transactions), nil
		}
	}
	var one modelDraft
	if err := json.Unmarshal([]byte(text), &one); err != nil {
		return nil, err
	}
	if len(one.Amount) == 0 && one.Kind == "" && one.Note == "" {
		return nil, fmt.Errorf("empty model draft")
	}
	return []modelDraft{one}, nil
}

func capDrafts(drafts []modelDraft) []modelDraft {
	if len(drafts) > recognizeMaxTx {
		return drafts[:recognizeMaxTx]
	}
	return drafts
}

func bindDrafts(drafts []modelDraft, categories []store.Category, activities []store.Activity, cards []store.Card) (recognizeOut, error) {
	out := recognizeOut{Entries: []recognizeEntry{}}
	for _, draft := range drafts {
		entry, err := bindDraft(draft, categories, activities, cards)
		if err != nil {
			continue
		}
		out.Entries = append(out.Entries, entry)
	}
	if len(out.Entries) == 0 {
		return recognizeOut{}, fmt.Errorf("没有识别到账目")
	}
	return out, nil
}

func bindDraft(draft modelDraft, categories []store.Category, activities []store.Activity, cards []store.Card) (recognizeEntry, error) {
	cents, err := parseModelAmount(draft.Amount)
	if err != nil {
		return recognizeEntry{}, err
	}
	date := strings.TrimSpace(draft.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return recognizeEntry{}, fmt.Errorf("识别出的日期无效")
	}
	kind, id := resolveCategory(draft, categories)
	if id == 0 {
		return recognizeEntry{}, fmt.Errorf("无法对应到现有分类")
	}
	note := strings.TrimSpace(draft.Note)
	if utf8.RuneCountInString(note) > 100 {
		note = string([]rune(note)[:100])
	}
	return recognizeEntry{
		Kind:       kind,
		Amount:     cents,
		CategoryID: id,
		ActivityID: resolveActivityID(draft, activities),
		CardID:     resolveCardID(draft, cards),
		Date:       date,
		Note:       note,
		Shared:     draft.Shared,
	}, nil
}

func resolveActivityID(draft modelDraft, activities []store.Activity) int64 {
	if id := liveActivityID(draft.ActivityID, activities); id != 0 {
		return id
	}
	if id := matchActivityName(draft.ActivityName, activities); id != 0 {
		return id
	}
	for _, activity := range activities {
		if activity.IsDefault {
			return activity.ID
		}
	}
	return 0
}

func liveActivityID(id int64, activities []store.Activity) int64 {
	if id == 0 {
		return 0
	}
	for _, activity := range activities {
		if activity.ID == id {
			return id
		}
	}
	return 0
}

func matchActivityName(name string, activities []store.Activity) int64 {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0
	}
	var fuzzy int64
	for _, activity := range activities {
		if activity.Name == name {
			return activity.ID
		}
		if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(activity.Name) < 2 {
			continue
		}
		if strings.Contains(name, activity.Name) || strings.Contains(activity.Name, name) {
			if fuzzy == 0 || utf8.RuneCountInString(activity.Name) > utf8.RuneCountInString(activityName(activities, fuzzy)) {
				fuzzy = activity.ID
			}
		}
	}
	return fuzzy
}

func activityName(activities []store.Activity, id int64) string {
	for _, activity := range activities {
		if activity.ID == id {
			return activity.Name
		}
	}
	return ""
}

func resolveCardID(draft modelDraft, cards []store.Card) int64 {
	if id := liveCardID(draft.CardID, cards); id != 0 {
		return id
	}
	if last4 := last4Of(draft.CardLast4, draft.CardName); last4 != "" {
		var hit int64
		n := 0
		for _, card := range cards {
			if card.Archived || card.Last4 != last4 {
				continue
			}
			n++
			hit = card.ID
		}
		if n == 1 {
			return hit
		}
	}
	return matchCardName(draft.CardName, cards)
}

func liveCardID(id int64, cards []store.Card) int64 {
	if id == 0 {
		return 0
	}
	for _, card := range cards {
		if !card.Archived && card.ID == id {
			return id
		}
	}
	return 0
}

func last4Of(parts ...string) string {
	for _, part := range parts {
		digits := make([]rune, 0, 4)
		for _, r := range part {
			if r >= '0' && r <= '9' {
				digits = append(digits, r)
			}
		}
		if len(digits) >= 4 {
			return string(digits[len(digits)-4:])
		}
	}
	return ""
}

func matchCardName(name string, cards []store.Card) int64 {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 2 {
		return 0
	}
	var hit int64
	n := 0
	for _, card := range cards {
		if card.Archived {
			continue
		}
		label := cardHint(card.Bank, card.Name, card.Last4)
		if card.Bank == name || card.Name == name || label == name || strings.Contains(label, name) || strings.Contains(name, card.Bank) && card.Bank != "" {
			n++
			hit = card.ID
		}
	}
	if n == 1 {
		return hit
	}
	return 0
}

func resolveCategory(draft modelDraft, categories []store.Category) (string, int64) {
	if cat := liveCategory(draft.CategoryID, "", categories); cat != nil {
		return cat.Kind, cat.ID
	}
	kind := draft.Kind
	if checkKindJSON(kind) != nil {
		kind = ""
	}
	if cat := matchCategoryName(draft.CategoryName, kind, categories); cat != nil {
		return cat.Kind, cat.ID
	}
	if kind == "" {
		kind = store.KindExpense
	}
	if id := fallbackCategory(kind, categories); id != 0 {
		return kind, id
	}
	return "", 0
}

func liveCategory(id int64, kind string, categories []store.Category) *store.Category {
	if id == 0 {
		return nil
	}
	for i := range categories {
		category := &categories[i]
		if category.ID != id || category.Archived {
			continue
		}
		if kind == "" || category.Kind == kind {
			return category
		}
	}
	return nil
}

func matchCategoryName(name, kind string, categories []store.Category) *store.Category {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if cat := namedCategory(name, kind, true, categories); cat != nil {
		return cat
	}
	if cat := namedCategory(name, "", true, categories); cat != nil {
		return cat
	}
	if cat := namedCategory(name, kind, false, categories); cat != nil {
		return cat
	}
	return namedCategory(name, "", false, categories)
}

func namedCategory(name, kind string, exact bool, categories []store.Category) *store.Category {
	var best *store.Category
	for i := range categories {
		category := &categories[i]
		if category.Archived || (kind != "" && category.Kind != kind) {
			continue
		}
		if exact {
			if category.Name != name {
				continue
			}
			return category
		}
		if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(category.Name) < 2 {
			continue
		}
		if !strings.Contains(name, category.Name) && !strings.Contains(category.Name, name) {
			continue
		}
		if best == nil || utf8.RuneCountInString(category.Name) > utf8.RuneCountInString(best.Name) {
			best = category
		}
	}
	return best
}

func fallbackCategory(kind string, categories []store.Category) int64 {
	for _, category := range categories {
		if !category.Archived && category.Kind == kind && category.Name == "其他" {
			return category.ID
		}
	}
	for _, category := range categories {
		if !category.Archived && category.Kind == kind {
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
