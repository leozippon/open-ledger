package server

import (
	"encoding/csv"
	"log"
	"net/http"
	"strings"
	"time"

	"ledger/internal/money"
	"ledger/internal/store"
)

// txBody is the writable shape of a transaction on the wire. The recording
// member comes from the session, never from the request.
type txBody struct {
	Kind       string      `json:"kind"`
	Amount     money.Cents `json:"amount"`
	CategoryID int64       `json:"category_id"`
	ActivityID int64       `json:"activity_id"`
	CardID     int64       `json:"card_id"`
	ToCardID   int64       `json:"to_card_id"`
	Date       string      `json:"date"`
	Note       string      `json:"note"`
	Shared     bool        `json:"shared"`
	Currency   string      `json:"currency"`
	ToAmount   money.Cents `json:"to_amount"`
	ToCurrency string      `json:"to_currency"`
}

func (b txBody) input() store.TxInput {
	return store.TxInput{
		Kind:       b.Kind,
		Amount:     int64(b.Amount),
		CategoryID: b.CategoryID,
		ActivityID: b.ActivityID,
		CardID:     b.CardID,
		ToCardID:   b.ToCardID,
		Date:       b.Date,
		Note:       b.Note,
		Shared:     b.Shared,
		Currency:   b.Currency,
		ToAmount:   int64(b.ToAmount),
		ToCurrency: b.ToCurrency,
	}
}

func (s *Server) listTransactions(w http.ResponseWriter, r *http.Request, sess session) {
	q := r.URL.Query()
	filter := store.TxFilter{
		Month:      q.Get("month"),
		Kind:       q.Get("kind"),
		CategoryID: queryInt(r, "category_id"),
		ActivityID: queryInt(r, "activity_id"),
		UserID:     queryInt(r, "user_id"),
		SharedOnly: queryFlag(r, "shared"),
		Query:      q.Get("q"),
		MovesOnly:  queryFlag(r, "moves"),
	}
	if filter.Month == "" {
		filter.Month = time.Now().Format("2006-01")
	}
	txs, err := sess.Book.Transactions(filter)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, txs)
}

func (s *Server) createTransaction(w http.ResponseWriter, r *http.Request, sess session) {
	var body txBody
	if !decodeBody(w, r, &body) {
		return
	}
	in := body.input()
	in.UserID = sess.User.ID
	tx, err := sess.Book.CreateTransaction(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tx)
}

// updateTransaction lets any member correct any entry: a family ledger is a
// shared document. The original recorder is kept.
func (s *Server) updateTransaction(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body txBody
	if !decodeBody(w, r, &body) {
		return
	}
	tx, err := sess.Book.UpdateTransaction(id, body.input())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tx)
}

func (s *Server) deleteTransaction(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := sess.Book.DeleteTransaction(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request, sess session) {
	userID := queryInt(r, "user_id")
	shared := queryFlag(r, "shared")
	activityID := queryInt(r, "activity_id")
	q := r.URL.Query()
	if queryFlag(r, "all") {
		sum, err := sess.Book.SummaryAll(userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sum)
		return
	}
	if from, to := q.Get("from"), q.Get("to"); from != "" || to != "" {
		sum, err := sess.Book.SummaryWindow(from, to, userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sum)
		return
	}
	if year := q.Get("year"); year != "" {
		sum, err := sess.Book.SummaryYearFiltered(year, userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sum)
		return
	}
	month := q.Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	sum, err := sess.Book.SummaryFiltered(month, userID, shared, activityID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) trend(w http.ResponseWriter, r *http.Request, sess session) {
	userID := queryInt(r, "user_id")
	shared := queryFlag(r, "shared")
	activityID := queryInt(r, "activity_id")
	q := r.URL.Query()
	if queryFlag(r, "all") {
		points, err := sess.Book.TrendAll(userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, points)
		return
	}
	if from, to := q.Get("from"), q.Get("to"); from != "" || to != "" {
		points, err := sess.Book.TrendWindow(from, to, userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, points)
		return
	}
	if year := q.Get("year"); year != "" {
		points, err := sess.Book.TrendYearFiltered(year, userID, shared, activityID)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, points)
		return
	}
	months := int(queryInt(r, "months"))
	if months == 0 {
		months = 12
	}
	points, err := sess.Book.TrendFiltered(months, userID, shared, activityID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, points)
}

func (s *Server) listActivities(w http.ResponseWriter, _ *http.Request, sess session) {
	items, err := sess.Book.Activities()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createActivity(w http.ResponseWriter, r *http.Request, sess session) {
	var body store.Activity
	if !decodeBody(w, r, &body) {
		return
	}
	item, err := sess.Book.CreateActivity(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) reorderActivities(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := sess.Book.ReorderActivities(body.IDs); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) updateActivity(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body store.Activity
	if !decodeBody(w, r, &body) {
		return
	}
	item, err := sess.Book.UpdateActivity(id, body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteActivity(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := sess.Book.DeleteActivity(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) activityMonths(w http.ResponseWriter, r *http.Request, sess session) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	items, err := sess.Book.ActivityMonths(month, queryInt(r, "user_id"), queryFlag(r, "shared"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) searchNotes(w http.ResponseWriter, r *http.Request, sess session) {
	out, err := sess.Book.SearchNotes(r.URL.Query().Get("q"), queryInt(r, "user_id"), queryFlag(r, "shared"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listCategories(w http.ResponseWriter, _ *http.Request, sess session) {
	categories, err := sess.Book.Categories()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, categories)
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request, sess session) {
	var body store.Category
	if !decodeBody(w, r, &body) {
		return
	}
	category, err := sess.Book.CreateCategory(body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, category)
}

func (s *Server) reorderCategories(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		Kind string  `json:"kind"`
		IDs  []int64 `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := sess.Book.ReorderCategories(body.Kind, body.IDs); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) updateCategory(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body store.Category
	if !decodeBody(w, r, &body) {
		return
	}
	category, err := sess.Book.UpdateCategory(id, body)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, category)
}

type cardBody struct {
	Kind      string           `json:"kind"`
	Bank      string           `json:"bank"`
	Name      string           `json:"name"`
	Last4     string           `json:"last4"`
	Network   string           `json:"network"`
	Archived  bool             `json:"archived"`
	SortOrder int              `json:"sort_order"`
	Currency  string           `json:"currency,omitempty"`
	Balance   *int64           `json:"balance,omitempty"`
	Funds     []store.CardFund `json:"funds,omitempty"`
}

func (b cardBody) card() store.Card {
	return store.Card{
		Kind: b.Kind, Bank: b.Bank, Name: b.Name, Last4: b.Last4, Network: b.Network,
		Archived: b.Archived, SortOrder: b.SortOrder, Currency: b.Currency, Funds: b.Funds,
	}
}

func (s *Server) listCards(w http.ResponseWriter, _ *http.Request, sess session) {
	cards, err := sess.Book.Cards()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cards)
}

func (s *Server) createCard(w http.ResponseWriter, r *http.Request, sess session) {
	var body cardBody
	if !decodeBody(w, r, &body) {
		return
	}
	in := body.card()
	if body.Balance != nil {
		in.Balance = *body.Balance
	}
	card, err := sess.Book.CreateCard(in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, card)
}

func (s *Server) reorderCards(w http.ResponseWriter, r *http.Request, sess session) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := sess.Book.ReorderCards(body.IDs); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) updateCard(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body cardBody
	if !decodeBody(w, r, &body) {
		return
	}
	card, err := sess.Book.UpdateCard(id, body.card(), body.Balance)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (s *Server) deleteCard(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := sess.Book.DeleteCard(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request, sess session) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := sess.Book.DeleteCategory(id); err != nil {
		s.fail(w, err)
		return
	}
	writeOK(w)
}

type settingsBody struct {
	MonthlyBudget *int64 `json:"monthly_budget,omitempty"`
}

type settingsOut struct {
	MonthlyBudget int64 `json:"monthly_budget"`
}

func (s *Server) writeSettings(w http.ResponseWriter, sess session) {
	budget, err := sess.Book.Budget()
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, settingsOut{MonthlyBudget: budget})
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request, sess session) {
	s.writeSettings(w, sess)
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, sess session) {
	var body settingsBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.MonthlyBudget != nil {
		if err := sess.Book.SetBudget(*body.MonthlyBudget); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.writeSettings(w, sess)
}

var kindLabels = map[string]string{
	store.KindExpense:  "支出",
	store.KindIncome:   "收入",
	store.KindTransfer: "转账",
	store.KindExchange: "兑换",
}

func (s *Server) exportCSV(w http.ResponseWriter, _ *http.Request, sess session) {
	txs, err := sess.Book.AllTransactions()
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ledger-`+time.Now().Format("20060102")+`.csv"`)
	// Excel needs the BOM to detect UTF-8.
	if _, err := w.Write([]byte("\xef\xbb\xbf")); err != nil {
		return
	}
	out := csv.NewWriter(w)
	rows := [][]string{{"日期", "类型", "分类", "活动", "金额", "货币", "银行卡", "转入卡", "兑入金额", "兑入货币", "备注", "共同", "记录人"}}
	for _, t := range txs {
		shared := "否"
		if t.Shared {
			shared = "是"
		}
		toAmount := ""
		if t.ToAmount > 0 {
			toAmount = money.FormatYuan(t.ToAmount)
		}
		rows = append(rows, []string{
			t.Date, kindLabels[t.Kind], t.CategoryName, t.ActivityName,
			money.FormatYuan(t.Amount), t.Currency, csvCard(t.CardBank, t.CardName, t.CardLast4), csvCard(t.ToCardBank, t.ToCardName, t.ToCardLast4),
			toAmount, t.ToCurrency, t.Note, shared, t.Username,
		})
	}
	if err := out.WriteAll(rows); err != nil {
		log.Printf("ledger: write csv: %v", err)
	}
}

func csvCard(bank, name, last4 string) string {
	head := strings.TrimSpace(bank + " " + name)
	if head == "" {
		return ""
	}
	if last4 == "" {
		return head
	}
	return head + " · " + last4
}
