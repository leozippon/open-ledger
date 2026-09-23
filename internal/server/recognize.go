package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
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
	Currency   string `json:"currency"`
	ToAmount   int64  `json:"to_amount"`
	ToCurrency string `json:"to_currency"`
	CategoryID int64  `json:"category_id"`
	ActivityID int64  `json:"activity_id"`
	CardID     int64  `json:"card_id"`
	ToCardID   int64  `json:"to_card_id"`
	Date       string `json:"date"`
	Note       string `json:"note"`
	Shared     bool   `json:"shared"`
}

type modelDraft struct {
	Kind         string          `json:"kind"`
	Amount       json.RawMessage `json:"amount"`
	Currency     string          `json:"currency"`
	ToAmount     json.RawMessage `json:"to_amount"`
	ToCurrency   string          `json:"to_currency"`
	CategoryID   int64           `json:"category_id"`
	CategoryName string          `json:"category_name"`
	ActivityID   int64           `json:"activity_id"`
	ActivityName string          `json:"activity_name"`
	CardID       int64           `json:"card_id"`
	CardName     string          `json:"card_name"`
	CardLast4    string          `json:"card_last4"`
	ToCardID     int64           `json:"to_card_id"`
	ToCardName   string          `json:"to_card_name"`
	ToCardLast4  string          `json:"to_card_last4"`
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
	out.Entries = omitRepeated(out.Entries, recent)
	if len(out.Entries) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "没有识别到账目")
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
		"max_tokens":      4000,
	})
	if err != nil {
		return nil, err
	}

	base := strings.TrimRight(s.cfg.DeepSeekURL, "/")
	if base == "" {
		base = deepSeekDefault
	}
	var last error
	for attempt := 1; attempt <= 2; attempt++ {
		drafts, err := s.postDeepSeek(ctx, base+"/chat/completions", payload)
		if err == nil {
			return drafts, nil
		}
		last = err
		if attempt == 2 || !strings.Contains(err.Error(), "parse model json") {
			return nil, err
		}
		log.Printf("ledger: recognize: retry after %v", err)
	}
	return nil, last
}

func (s *Server) postDeepSeek(ctx context.Context, url string, payload []byte) ([]modelDraft, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
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
		return nil, fmt.Errorf("deepseek status %d: %s", resp.StatusCode, apiErrorMessage(body))
	}

	var envelope struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("empty deepseek response")
	}
	choice := envelope.Choices[0]
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		content = strings.TrimSpace(choice.Message.Reasoning)
	}
	drafts, err := parseModelJSON(content)
	if err != nil {
		return nil, fmt.Errorf("parse model json: %w finish=%s content=%q", err, choice.Finish, clip(content, 180))
	}
	return drafts, nil
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func apiErrorMessage(body []byte) string {
	var wrap struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &wrap) == nil && wrap.Error.Message != "" {
		msg := wrap.Error.Message
		if utf8.RuneCountInString(msg) > 160 {
			return string([]rune(msg)[:160])
		}
		return msg
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty body"
	}
	if utf8.RuneCountInString(text) > 160 {
		return string([]rune(text)[:160])
	}
	return text
}

const recognizeInstructions = `根据文字或图片整理家庭账本。

只整理这次文字或图片里的账单。一条对应一笔独立订单或一次独立付款。同一付款里的多件商品不要拆开；不同订单或不同付款不要合并。
结售汇或跨境汇款拆成相邻两笔：先在汇出卡上兑换，再把买入的货币转到收款卡。这不是支出或收入。手续费为零则不另记。

只输出一个 JSON 对象，每个键名都加双引号。例如 {"entries":[{"kind":"exchange","amount":100.00,"currency":"CNY","to_amount":110.00,"to_currency":"HKD","card_name":"汇出卡","date":"2026-09-23","note":"购汇","shared":false}]}。
编号必须来自下列分类、活动和银行卡，不要沿用例子里的数字。
kind 为 expense、income、exchange 或 transfer。金额写数字且必须大于 0，不要千分位逗号；货币用 CNY、HKD、USD 这类代码。
支出和收入：amount 为人民币元。category_id、activity_id 必须是下列编号，每笔单独选；活动看不出则选默认。card_id 能对应则填，看不出则 0。
兑换：amount 与 currency 是卖出，to_amount 与 to_currency 是买入，发生在 card_id 这一张卡上。
转账：amount 与 currency 从 card_id 转到 to_card_id，两张卡都要从下列银行卡里对应上。
date 为 YYYY-MM-DD。note 简短，只写这一笔；没有则空字符串。
shared 在全家一起时为 true，个人、兑换、转账或看不出时为 false。
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
	b.WriteString("\n已记备注\n")
	b.WriteString("这些已经入账，不要输出，只可模仿用词。\n")
	seen := map[string]bool{}
	n := 0
	for _, tx := range recent {
		note := strings.TrimSpace(tx.Note)
		if note == "" || seen[note] {
			continue
		}
		seen[note] = true
		b.WriteString(note)
		b.WriteByte('\n')
		n++
		if n == 12 {
			break
		}
	}
	if n == 0 {
		b.WriteString("（暂无）\n")
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
	head := "只整理这次账单。今天是 " + now.Format("2006-01-02") + "。"
	if text == "" {
		return head
	}
	return head + "\n" + text
}

var (
	bareKey   = regexp.MustCompile(`([{\[,]\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:`)
	thousands = regexp.MustCompile(`(\d),(\d{3})`)
)

func parseModelJSON(raw string) ([]modelDraft, error) {
	text := extractJSON(raw)
	drafts, err := decodeDrafts(text)
	if err == nil {
		return drafts, nil
	}
	if loose := loosenModelJSON(text); loose != text {
		if drafts, err2 := decodeDrafts(loose); err2 == nil {
			return drafts, nil
		}
	}
	return nil, err
}

func neutralizeNulls(text string) string {
	for _, key := range []string{"card_id", "to_card_id", "category_id", "activity_id"} {
		text = strings.ReplaceAll(text, `"`+key+`":null`, `"`+key+`":0`)
		text = strings.ReplaceAll(text, `"`+key+`": null`, `"`+key+`":0`)
	}
	return text
}

func loosenModelJSON(text string) string {
	text = bareKey.ReplaceAllString(text, `$1"$2":`)
	for prev := ""; prev != text; {
		prev = text
		text = thousands.ReplaceAllString(text, `$1$2`)
	}
	return text
}

func decodeDrafts(text string) ([]modelDraft, error) {
	text = neutralizeNulls(text)
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

func omitRepeated(entries []recognizeEntry, recent []store.Transaction) []recognizeEntry {
	kept := make([]recognizeEntry, 0, len(entries))
	for _, entry := range entries {
		if remittanceAsCash(entry) || alreadyRecorded(entry, recent) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func remittanceAsCash(entry recognizeEntry) bool {
	if entry.Kind != store.KindExpense && entry.Kind != store.KindIncome {
		return false
	}
	switch strings.TrimSpace(entry.Note) {
	case "购汇", "换汇", "结售汇", "跨境汇款", "跨境支付":
		return true
	default:
		return false
	}
}

func alreadyRecorded(entry recognizeEntry, recent []store.Transaction) bool {
	note := strings.TrimSpace(entry.Note)
	if note == "" {
		return false
	}
	for _, tx := range recent {
		if tx.Kind == entry.Kind && tx.Amount == entry.Amount && tx.Date == entry.Date && strings.TrimSpace(tx.Note) == note {
			return true
		}
	}
	return false
}

func capDrafts(drafts []modelDraft) []modelDraft {
	if len(drafts) > recognizeMaxTx {
		return drafts[:recognizeMaxTx]
	}
	return drafts
}

func bindDrafts(drafts []modelDraft, categories []store.Category, activities []store.Activity, cards []store.Card) (recognizeOut, error) {
	type row struct {
		kind  string
		entry recognizeEntry
		ok    bool
	}
	rows := make([]row, len(drafts))
	for i, draft := range drafts {
		rows[i].kind = draft.Kind
		entry, err := bindDraft(draft, categories, activities, cards)
		if err != nil {
			continue
		}
		rows[i].entry = entry
		rows[i].ok = true
		rows[i].kind = entry.Kind
	}
	for i := range rows {
		if rows[i].ok && rows[i].entry.Kind == store.KindExchange && i+1 < len(rows) && rows[i+1].kind == store.KindTransfer && !rows[i+1].ok {
			rows[i].ok = false
		}
	}
	for i := range rows {
		if rows[i].ok && rows[i].entry.Kind == store.KindTransfer && i > 0 && rows[i-1].kind == store.KindExchange && !rows[i-1].ok {
			rows[i].ok = false
		}
	}
	out := recognizeOut{Entries: []recognizeEntry{}}
	for _, item := range rows {
		if item.ok {
			out.Entries = append(out.Entries, item.entry)
		}
	}
	if len(out.Entries) == 0 {
		return recognizeOut{}, fmt.Errorf("没有识别到账目")
	}
	return out, nil
}

func bindDraft(draft modelDraft, categories []store.Category, activities []store.Activity, cards []store.Card) (recognizeEntry, error) {
	date, note, err := draftWhen(draft)
	if err != nil {
		return recognizeEntry{}, err
	}
	switch draft.Kind {
	case store.KindExchange:
		return bindExchange(draft, cards, date, note)
	case store.KindTransfer:
		return bindTransfer(draft, cards, date, note)
	default:
		return bindCash(draft, categories, activities, cards, date, note)
	}
}

func draftWhen(draft modelDraft) (string, string, error) {
	date := strings.TrimSpace(draft.Date)
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", "", fmt.Errorf("识别出的日期无效")
	}
	note := strings.TrimSpace(draft.Note)
	if utf8.RuneCountInString(note) > 100 {
		note = string([]rune(note)[:100])
	}
	return date, note, nil
}

func bindCash(draft modelDraft, categories []store.Category, activities []store.Activity, cards []store.Card, date, note string) (recognizeEntry, error) {
	cents, err := parseModelAmount(draft.Amount)
	if err != nil {
		return recognizeEntry{}, err
	}
	kind, id := resolveCategory(draft, categories)
	if id == 0 {
		return recognizeEntry{}, fmt.Errorf("无法对应到现有分类")
	}
	return recognizeEntry{
		Kind:       kind,
		Amount:     cents,
		Currency:   store.CurrencyCNY,
		CategoryID: id,
		ActivityID: resolveActivityID(draft, activities),
		CardID:     resolveCardRef(draft.CardID, draft.CardName, draft.CardLast4, cards),
		Date:       date,
		Note:       note,
		Shared:     draft.Shared,
	}, nil
}

func bindExchange(draft modelDraft, cards []store.Card, date, note string) (recognizeEntry, error) {
	sell, err := parseModelAmount(draft.Amount)
	if err != nil {
		return recognizeEntry{}, err
	}
	buy, err := parseModelAmount(draft.ToAmount)
	if err != nil {
		return recognizeEntry{}, err
	}
	sellCode, ok := canonicalCurrency(draft.Currency)
	if !ok {
		return recognizeEntry{}, fmt.Errorf("无法对应卖出货币")
	}
	buyCode, ok := canonicalCurrency(draft.ToCurrency)
	if !ok || buyCode == sellCode {
		return recognizeEntry{}, fmt.Errorf("无法对应买入货币")
	}
	cardID := resolveCardRef(draft.CardID, draft.CardName, draft.CardLast4, cards)
	if cardID == 0 {
		cardID = resolveCardRef(0, note, note, cards)
	}
	if cardID == 0 {
		return recognizeEntry{}, fmt.Errorf("无法对应银行卡")
	}
	return recognizeEntry{
		Kind:       store.KindExchange,
		Amount:     sell,
		Currency:   sellCode,
		ToAmount:   buy,
		ToCurrency: buyCode,
		CardID:     cardID,
		Date:       date,
		Note:       note,
	}, nil
}

func bindTransfer(draft modelDraft, cards []store.Card, date, note string) (recognizeEntry, error) {
	cents, err := parseModelAmount(draft.Amount)
	if err != nil {
		return recognizeEntry{}, err
	}
	code, ok := canonicalCurrency(draft.Currency)
	if !ok {
		code = store.CurrencyCNY
	}
	fromID := resolveCardRef(draft.CardID, draft.CardName, draft.CardLast4, cards)
	toID := resolveCardRef(draft.ToCardID, draft.ToCardName, draft.ToCardLast4, cards)
	if fromID == 0 {
		fromID = resolveCardRef(0, "", note, cards)
	}
	if toID == 0 {
		toID = matchCardName(note, cards)
	}
	if toID == fromID {
		toID = 0
	}
	if fromID == 0 || toID == 0 {
		return recognizeEntry{}, fmt.Errorf("无法对应转账银行卡")
	}
	return recognizeEntry{
		Kind:     store.KindTransfer,
		Amount:   cents,
		Currency: code,
		CardID:   fromID,
		ToCardID: toID,
		Date:     date,
		Note:     note,
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

func resolveCardRef(id int64, name, last4 string, cards []store.Card) int64 {
	if live := liveCardID(id, cards); live != 0 {
		return live
	}
	if tail := last4Of(last4, name); tail != "" {
		var hit int64
		n := 0
		for _, card := range cards {
			if card.Archived || card.Last4 != tail {
				continue
			}
			n++
			hit = card.ID
		}
		if n == 1 {
			return hit
		}
	}
	return matchCardName(name, cards)
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
	folded := strings.ToLower(name)
	var hit int64
	n := 0
	for _, card := range cards {
		if card.Archived || !cardMatches(card, name, folded) {
			continue
		}
		n++
		hit = card.ID
	}
	if n == 1 {
		return hit
	}
	return 0
}

func cardMatches(card store.Card, name, folded string) bool {
	label := cardHint(card.Bank, card.Name, card.Last4)
	if card.Bank == name || card.Name == name || label == name || strings.Contains(label, name) || strings.Contains(name, card.Bank) && card.Bank != "" {
		return true
	}
	for _, alias := range bankAliases {
		if strings.Contains(folded, alias.needle) && card.Bank == alias.bank {
			return true
		}
	}
	return false
}

// bankAliases covers English names that receipts use for a Chinese bank already in the book.
var bankAliases = []struct{ needle, bank string }{
	{"za bank", "众安银行"},
}

func canonicalCurrency(raw string) (string, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", false
	}
	upper := strings.ToUpper(text)
	for _, code := range store.Currencies {
		if upper == code {
			return code, true
		}
	}
	switch text {
	case "人民币", "元":
		return store.CurrencyCNY, true
	case "港币", "港元":
		return store.CurrencyHKD, true
	case "美元":
		return store.CurrencyUSD, true
	case "加元":
		return store.CurrencyCAD, true
	case "台币", "新台币":
		return store.CurrencyTWD, true
	case "欧元":
		return store.CurrencyEUR, true
	case "英镑":
		return store.CurrencyGBP, true
	case "日元":
		return store.CurrencyJPY, true
	case "澳元":
		return store.CurrencyAUD, true
	case "新币", "新加坡元":
		return store.CurrencySGD, true
	default:
		return "", false
	}
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
	}
	text = strings.ReplaceAll(text, ",", "")
	text = strings.ReplaceAll(text, "，", "")
	text = strings.TrimSpace(text)
	if n, err := strconv.ParseFloat(text, 64); err == nil {
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
