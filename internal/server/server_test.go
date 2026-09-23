package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"ledger/internal/books"
	"ledger/internal/server"
	"ledger/internal/store"
)

const (
	adminUser     = "mama"
	adminPassword = "family-secret"
)

func newServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	return newServerWith(t, server.Config{Secret: []byte("test-session-secret")})
}

func newServerWith(t *testing.T, cfg server.Config) (*httptest.Server, *http.Client) {
	t.Helper()
	reg, err := books.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open books: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	if _, err := reg.Create(store.Admin{Username: adminUser, Password: adminPassword}); err != nil {
		t.Fatalf("create book: %v", err)
	}

	handler, err := server.New(reg, cfg)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, newClient(t)
}

func newEmptyServer(t *testing.T, cfg server.Config) (*httptest.Server, *books.Books, *http.Client) {
	t.Helper()
	if len(cfg.Secret) == 0 {
		cfg.Secret = []byte("test-session-secret")
	}
	reg, err := books.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open books: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	handler, err := server.New(reg, cfg)
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, reg, newClient(t)
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func do(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response, payload
}

func login(t *testing.T, client *http.Client, srv *httptest.Server, username, password string) *http.Response {
	t.Helper()
	response, _ := do(t, client, http.MethodPost, srv.URL+"/api/login",
		map[string]string{"username": username, "password": password})
	return response
}

func mustLogin(t *testing.T, client *http.Client, srv *httptest.Server, username, password string) {
	t.Helper()
	if response := login(t, client, srv, username, password); response.StatusCode != http.StatusOK {
		t.Fatalf("login as %q = %d, want 200", username, response.StatusCode)
	}
}

func decode[T any](t *testing.T, payload []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("decode %s: %v", payload, err)
	}
	return value
}

func TestRoundTrip(t *testing.T) {
	srv, client := newServer(t)

	// Everything under /api needs a session, and the SPA shell does not.
	if response, _ := do(t, client, http.MethodGet, srv.URL+"/api/me", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/me before login = %d, want 401", response.StatusCode)
	}
	if response, _ := do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/summary before login = %d, want 401", response.StatusCode)
	}
	response, page := do(t, client, http.MethodGet, srv.URL+"/", nil)
	if response.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("<title>记账</title>")) {
		t.Fatalf("GET / = %d, body %.60s", response.StatusCode, page)
	}

	if got := login(t, client, srv, adminUser, "wrong").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password = %d, want 401", got)
	}
	if got := login(t, client, srv, "nobody", adminPassword).StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("login as an unknown member = %d, want 401", got)
	}
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/me", nil)
	me := decode[map[string]any](t, payload)
	if me["username"] != adminUser || me["is_admin"] != true {
		t.Fatalf("GET /api/me = %s, want the bootstrap administrator", payload)
	}

	// The account dimension is gone; only categories group the entries.
	if response, _ = do(t, client, http.MethodGet, srv.URL+"/api/accounts", nil); response.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/accounts = %d, want 404", response.StatusCode)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	categories := decode[[]store.Category](t, payload)
	if len(categories) == 0 {
		t.Fatal("seeded categories are missing")
	}
	food := pickCategory(t, categories, store.KindExpense)
	salary := pickCategory(t, categories, store.KindIncome)

	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 1250, "category_id": food,
		"date": "2026-03-04", "note": "午饭",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create expense = %d, body %s", response.StatusCode, payload)
	}
	created := decode[store.Transaction](t, payload)
	if created.Amount != 1250 || created.CategoryName == "" || created.Shared {
		t.Fatalf("created entry looks wrong: %+v", created)
	}
	// The recorder comes from the session, not from the request.
	if created.Username != adminUser {
		t.Errorf("created entry is attributed to %q, want %q", created.Username, adminUser)
	}

	// A yuan string is accepted as well, so the API stays usable by hand.
	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "income", "amount": "8000.5", "category_id": salary,
		"date": "2026-03-10",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("create income = %d, body %s", response.StatusCode, payload)
	}
	if got := decode[store.Transaction](t, payload).Amount; got != 800050 {
		t.Errorf("income amount = %d, want 800050", got)
	}

	// A transfer without two cards is still rejected.
	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "transfer", "amount": 5000, "date": "2026-03-11",
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("create transfer without cards = %d, want 400 (%s)", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	summary := decode[store.Summary](t, payload)
	if summary.Expense != 1250 || summary.Income != 800050 {
		t.Errorf("summary = %d/%d, want 1250/800050", summary.Expense, summary.Income)
	}
	if len(summary.ExpenseByCategory) != 1 {
		t.Errorf("expense breakdown = %+v, want one row", summary.ExpenseByCategory)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/trend?months=6", nil)
	if got := len(decode[[]store.TrendPoint](t, payload)); got != 6 {
		t.Errorf("trend has %d points, want 6", got)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/trend?year=2026", nil)
	yearTrend := decode[[]store.TrendPoint](t, payload)
	if len(yearTrend) != 12 || yearTrend[2].Month != "2026-03" {
		t.Errorf("year trend = %+v, want 12 months of 2026", yearTrend)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?year=2026", nil)
	yearSum := decode[store.Summary](t, payload)
	if yearSum.Expense != 1250 || yearSum.Income != 800050 {
		t.Errorf("year summary = %d/%d, want 1250/800050", yearSum.Expense, yearSum.Income)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?all=1", nil)
	if got := decode[store.Summary](t, payload).Expense; got != yearSum.Expense {
		t.Errorf("all summary expense = %d, want %d", got, yearSum.Expense)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/trend?all=1", nil)
	if allTrend := decode[[]store.TrendPoint](t, payload); len(allTrend) == 0 || allTrend[len(allTrend)-1].Month != "2026" {
		t.Errorf("all trend = %+v, want a 2026 point", allTrend)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/transactions?month=2026-03&kind=expense", nil)
	if got := len(decode[[]store.Transaction](t, payload)); got != 1 {
		t.Errorf("expense-only list has %d entries, want 1", got)
	}

	response, payload = do(t, client, http.MethodGet, srv.URL+"/api/export.csv", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export = %d", response.StatusCode)
	}
	if !bytes.HasPrefix(payload, []byte("\xef\xbb\xbf")) {
		t.Error("export is missing the UTF-8 BOM")
	}
	text := string(payload)
	if !strings.Contains(text, "日期,类型,分类,活动,金额,货币,银行卡,转入卡,兑入金额,兑入货币,备注,共同,记录人") {
		t.Errorf("export header is wrong: %.80s", text)
	}
	if !strings.Contains(text, "2026-03-04,支出,") || !strings.Contains(text, "12.50,CNY,,,,,午饭,否,"+adminUser) {
		t.Errorf("export is missing the expense row: %s", text)
	}

	// Refusing to delete a category that is in use is part of the contract.
	if response, _ = do(t, client, http.MethodDelete, srv.URL+"/api/categories/"+itoa(food), nil); response.StatusCode != http.StatusConflict {
		t.Errorf("delete used category = %d, want 409", response.StatusCode)
	}

	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 0, "category_id": food, "date": "2026-03-04",
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("zero amount = %d, want 400 (%s)", response.StatusCode, payload)
	}
	if decode[map[string]string](t, payload)["error"] == "" {
		t.Errorf("error response has no message: %s", payload)
	}

	if response, _ = do(t, client, http.MethodPost, srv.URL+"/api/logout", nil); response.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", response.StatusCode)
	}
	if response, _ = do(t, client, http.MethodGet, srv.URL+"/api/me", nil); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/me after logout = %d, want 401", response.StatusCode)
	}
}

func TestSharedExpenseAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	categories := decode[[]store.Category](t, payload)
	food := pickCategory(t, categories, store.KindExpense)
	salary := pickCategory(t, categories, store.KindIncome)

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 8800, "category_id": food,
		"date": "2026-03-04", "note": "聚餐", "shared": true,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create shared = %d, body %s", response.StatusCode, payload)
	}
	created := decode[store.Transaction](t, payload)
	if !created.Shared {
		t.Fatal("created expense is not shared")
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03&shared=1", nil)
	if got := decode[store.Summary](t, payload).Expense; got != 8800 {
		t.Errorf("shared summary = %d, want 8800", got)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	if got := decode[store.Summary](t, payload).Expense; got != 8800 {
		t.Errorf("family summary = %d, want 8800", got)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/me", nil)
	adminID := int64(decode[map[string]any](t, payload)["id"].(float64))
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03&user_id="+itoa(adminID), nil)
	if got := decode[store.Summary](t, payload).Expense; got != 0 {
		t.Errorf("personal summary after shared = %d, want 0", got)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/transactions?month=2026-03&shared=1", nil)
	if n := len(decode[[]store.Transaction](t, payload)); n != 1 {
		t.Errorf("shared list = %d, want 1", n)
	}

	response, payload = do(t, client, http.MethodPut, srv.URL+"/api/transactions/"+itoa(created.ID), map[string]any{
		"kind": "expense", "amount": 8800, "category_id": food,
		"date": "2026-03-04", "note": "聚餐", "shared": false,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clear shared = %d, body %s", response.StatusCode, payload)
	}
	if decode[store.Transaction](t, payload).Shared {
		t.Error("update left the expense shared")
	}

	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "income", "amount": 10000, "category_id": salary,
		"date": "2026-03-04", "shared": true,
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("shared income = %d, body %s", response.StatusCode, payload)
	}
	if !decode[store.Transaction](t, payload).Shared {
		t.Error("created income is not shared")
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03&shared=1", nil)
	shared := decode[store.Summary](t, payload)
	if shared.Expense != 0 || shared.Income != 10000 {
		t.Errorf("shared summary after income = %d/%d, want 0/10000", shared.Expense, shared.Income)
	}

	response, payload = do(t, client, http.MethodGet, srv.URL+"/api/export.csv", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("export = %d", response.StatusCode)
	}
	text := string(payload)
	if !strings.Contains(text, "聚餐,否,"+adminUser) {
		t.Errorf("export is missing the unmarked expense: %q", text)
	}
}

func TestBalanceAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	if sum := decode[store.Summary](t, payload); sum.Balance != 0 {
		t.Errorf("no-card balance = %d, want 0", sum.Balance)
	}

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "debit", "bank": "招商", "last4": "1234", "balance": 20000,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create debit = %d, body %s", response.StatusCode, payload)
	}
	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "credit", "bank": "中信", "last4": "8888", "balance": 3000,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create credit = %d, body %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	if sum := decode[store.Summary](t, payload); sum.Balance != 23000 {
		t.Errorf("sum of cards = %d, want 23000", sum.Balance)
	}

	if response, payload = do(t, client, http.MethodPut, srv.URL+"/api/settings", map[string]any{
		"balance": 99999,
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("ignore leftover balance field = %d, body %s", response.StatusCode, payload)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	if sum := decode[store.Summary](t, payload); sum.Balance != 23000 {
		t.Errorf("balance after ignored settings write = %d, want 23000", sum.Balance)
	}
}

func TestCardsAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "debit", "bank": "招商", "last4": "1234", "balance": 2000000,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create debit = %d, body %s", response.StatusCode, payload)
	}
	debit := decode[store.Card](t, payload)
	if debit.Balance != 2000000 || debit.Last4 != "1234" {
		t.Fatalf("created debit = %+v", debit)
	}

	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "credit", "bank": "中信", "last4": "8888",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create credit = %d, body %s", response.StatusCode, payload)
	}
	credit := decode[store.Card](t, payload)

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	var food int64
	for _, category := range decode[[]store.Category](t, payload) {
		if category.Kind == store.KindExpense && category.Name == "餐饮" {
			food = category.ID
		}
	}

	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 3500, "category_id": food, "date": "2026-03-04", "card_id": debit.ID,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("card expense = %d, body %s", response.StatusCode, payload)
	}

	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "transfer", "amount": 5000, "date": "2026-03-04",
		"card_id": debit.ID, "to_card_id": credit.ID,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("transfer = %d, body %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/cards", nil)
	cards := decode[[]store.Card](t, payload)
	byID := map[int64]store.Card{}
	for _, card := range cards {
		byID[card.ID] = card
	}
	if byID[debit.ID].Balance != 1991500 {
		t.Errorf("debit balance = %d, want 1991500", byID[debit.ID].Balance)
	}
	if byID[credit.ID].Balance != 5000 {
		t.Errorf("credit balance = %d, want 5000", byID[credit.ID].Balance)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	sum := decode[store.Summary](t, payload)
	if sum.Expense != 3500 || sum.Income != 0 {
		t.Errorf("summary after transfer = %d/%d, want 3500/0", sum.Expense, sum.Income)
	}
	if sum.Balance != 1996500 {
		t.Errorf("summary balance = %d, want 1996500", sum.Balance)
	}

	response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "exchange", "amount": 10000, "to_amount": 11000, "date": "2026-03-04",
		"card_id": debit.ID, "currency": "CNY", "to_currency": "HKD",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("exchange = %d, body %s", response.StatusCode, payload)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/cards", nil)
	for _, card := range decode[[]store.Card](t, payload) {
		if card.ID != debit.ID {
			continue
		}
		if card.Balance != 1981500 {
			t.Errorf("debit CNY after exchange = %d, want 1981500", card.Balance)
		}
		if fund := cardFund(card, "HKD"); fund != 11000 {
			t.Errorf("debit HKD after exchange = %d, want 11000", fund)
		}
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	sum = decode[store.Summary](t, payload)
	if sum.Balance != 1986500 {
		t.Errorf("summary CNY after exchange = %d, want 1986500", sum.Balance)
	}

	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "exchange", "amount": 100, "to_amount": 110, "date": "2026-03-04",
		"card_id": debit.ID, "currency": "USD", "to_currency": "USD",
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("same-currency exchange = %d, want 400 (%s)", response.StatusCode, payload)
	}

	if response, payload = do(t, client, http.MethodDelete, srv.URL+"/api/cards/"+itoa(debit.ID), nil); response.StatusCode != http.StatusConflict {
		t.Errorf("delete used card = %d, body %s, want 409", response.StatusCode, payload)
	}
}

func TestReorderCategoriesAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	categories := decode[[]store.Category](t, payload)
	ids := []int64{}
	for _, category := range categories {
		if category.Kind == store.KindExpense && !category.Archived {
			ids = append(ids, category.ID)
		}
	}
	if len(ids) < 2 {
		t.Fatal("need two expense categories to reorder")
	}
	first, second := ids[0], ids[1]
	ids[0], ids[1] = second, first

	response, payload := do(t, client, http.MethodPut, srv.URL+"/api/categories/order", map[string]any{
		"kind": "expense", "ids": ids,
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reorder = %d, body %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	listed := decode[[]store.Category](t, payload)
	active := []int64{}
	for _, category := range listed {
		if category.Kind == store.KindExpense && !category.Archived {
			active = append(active, category.ID)
		}
	}
	if active[0] != second || active[1] != first {
		t.Errorf("expense order = %v, want %d then %d", active, second, first)
	}

	if response, _ = do(t, client, http.MethodPut, srv.URL+"/api/categories/order", map[string]any{
		"kind": "expense", "ids": []int64{first},
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("partial reorder = %d, want 400", response.StatusCode)
	}
}

func TestReorderCardsAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	if response, payload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "debit", "bank": "招商", "last4": "1234",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("create first = %d, body %s", response.StatusCode, payload)
	}
	if response, payload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{
		"kind": "credit", "bank": "中信", "last4": "8888",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("create second = %d, body %s", response.StatusCode, payload)
	}

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/cards", nil)
	cards := decode[[]store.Card](t, payload)
	if len(cards) < 2 {
		t.Fatal("need two cards to reorder")
	}
	first, second := cards[0].ID, cards[1].ID

	response, payload := do(t, client, http.MethodPut, srv.URL+"/api/cards/order", map[string]any{
		"ids": []int64{second, first},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reorder = %d, body %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/cards", nil)
	listed := decode[[]store.Card](t, payload)
	if listed[0].ID != second || listed[1].ID != first {
		t.Errorf("card order = %v, want %d then %d", []int64{listed[0].ID, listed[1].ID}, second, first)
	}

	if response, _ = do(t, client, http.MethodPut, srv.URL+"/api/cards/order", map[string]any{
		"ids": []int64{first},
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("partial reorder = %d, want 400", response.StatusCode)
	}
}

func TestReorderActivitiesAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/activities", nil)
	seeded := decode[[]store.Activity](t, payload)
	if len(seeded) != 1 {
		t.Fatalf("seeded activities = %s", payload)
	}
	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/activities", map[string]any{
		"name": "香港之旅",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create activity = %d %s", response.StatusCode, payload)
	}
	trip := decode[store.Activity](t, payload)
	daily := seeded[0]

	response, payload = do(t, client, http.MethodPut, srv.URL+"/api/activities/order", map[string]any{
		"ids": []int64{trip.ID, daily.ID},
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("reorder = %d, body %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/activities", nil)
	listed := decode[[]store.Activity](t, payload)
	if len(listed) != 2 || listed[0].ID != trip.ID || listed[1].ID != daily.ID {
		t.Errorf("activity order = %+v, want 香港之旅 then 日常生活", listed)
	}

	if response, _ = do(t, client, http.MethodPut, srv.URL+"/api/activities/order", map[string]any{
		"ids": []int64{trip.ID},
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("partial reorder = %d, want 400", response.StatusCode)
	}
}

func TestFamilyMembers(t *testing.T) {
	srv, admin := newServer(t)
	mustLogin(t, admin, srv, adminUser, adminPassword)

	// Only administrators may change the roster.
	response, payload := do(t, admin, http.MethodPost, srv.URL+"/api/users", map[string]any{
		"username": "papa", "password": "another-secret",
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("add member = %d, body %s", response.StatusCode, payload)
	}
	member := decode[store.User](t, payload)
	if member.IsAdmin {
		t.Error("a plain member was created as an administrator")
	}
	if response, _ = do(t, admin, http.MethodPost, srv.URL+"/api/users", map[string]any{
		"username": "papa", "password": "another-secret",
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("duplicate username = %d, want 400", response.StatusCode)
	}

	papa := newClient(t)
	mustLogin(t, papa, srv, "papa", "another-secret")

	// Every member may read the roster; only administrators may change it.
	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/users", nil)
	if got := len(decode[[]store.User](t, payload)); got != 2 {
		t.Errorf("roster has %d members, want 2", got)
	}
	forbidden := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/users", map[string]any{"username": "didi", "password": "third-secret"}},
		{http.MethodPut, "/api/users/1/password", map[string]any{"password": "third-secret"}},
		{http.MethodDelete, "/api/users/1", nil},
	}
	for _, call := range forbidden {
		if response, _ = do(t, papa, call.method, srv.URL+call.path, call.body); response.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as a plain member = %d, want 403", call.method, call.path, response.StatusCode)
		}
	}

	// Both members share one ledger, and each entry keeps its recorder.
	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/categories", nil)
	food := pickCategory(t, decode[[]store.Category](t, payload), store.KindExpense)

	for _, entry := range []struct {
		client *http.Client
		amount int64
	}{{admin, 1000}, {papa, 2500}, {papa, 500}} {
		if response, payload = do(t, entry.client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
			"kind": "expense", "amount": entry.amount, "category_id": food, "date": "2026-03-04",
		}); response.StatusCode != http.StatusCreated {
			t.Fatalf("create entry = %d, body %s", response.StatusCode, payload)
		}
	}

	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/summary?month=2026-03", nil)
	if got := decode[store.Summary](t, payload).Expense; got != 4000 {
		t.Errorf("family expense = %d, want 4000", got)
	}
	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/summary?month=2026-03&user_id="+itoa(member.ID), nil)
	if got := decode[store.Summary](t, payload).Expense; got != 3000 {
		t.Errorf("papa's expense = %d, want 3000", got)
	}
	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/transactions?month=2026-03&user_id="+itoa(member.ID), nil)
	listed := decode[[]store.Transaction](t, payload)
	if len(listed) != 2 {
		t.Fatalf("papa's list has %d entries, want 2", len(listed))
	}
	for _, tx := range listed {
		if tx.Username != "papa" {
			t.Errorf("entry %d is attributed to %q, want papa", tx.ID, tx.Username)
		}
	}

	// A member who has recorded something stays, and nobody deletes themselves.
	if response, _ = do(t, admin, http.MethodDelete, srv.URL+"/api/users/"+itoa(member.ID), nil); response.StatusCode != http.StatusConflict {
		t.Errorf("delete a member with entries = %d, want 409", response.StatusCode)
	}
	stillThere := newClient(t)
	mustLogin(t, stillThere, srv, "papa", "another-secret")
	_, payload = do(t, admin, http.MethodGet, srv.URL+"/api/me", nil)
	adminID := int64(decode[map[string]any](t, payload)["id"].(float64))
	if response, _ = do(t, admin, http.MethodDelete, srv.URL+"/api/users/"+itoa(adminID), nil); response.StatusCode != http.StatusBadRequest {
		t.Errorf("delete yourself = %d, want 400", response.StatusCode)
	}

	// Changing a password ends the sessions that were opened with the old one.
	if response, payload = do(t, papa, http.MethodPut, srv.URL+"/api/me/password", map[string]any{
		"old_password": "wrong-secret", "new_password": "third-secret",
	}); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("change with a wrong current password = %d, want 401 (%s)", response.StatusCode, payload)
	}
	stale := newClient(t)
	mustLogin(t, stale, srv, "papa", "another-secret")
	if response, payload = do(t, papa, http.MethodPut, srv.URL+"/api/me/password", map[string]any{
		"old_password": "another-secret", "new_password": "third-secret",
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("change password = %d, body %s", response.StatusCode, payload)
	}
	if response, _ = do(t, stale, http.MethodGet, srv.URL+"/api/me", nil); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("a session opened before the password change = %d, want 401", response.StatusCode)
	}
	if got := login(t, papa, srv, "papa", "third-secret").StatusCode; got != http.StatusOK {
		t.Errorf("login with the new password = %d, want 200", got)
	}

	// An administrator reset also invalidates the member's sessions.
	if response, payload = do(t, admin, http.MethodPut, srv.URL+"/api/users/"+itoa(member.ID)+"/password", map[string]any{
		"password": "reset-secret",
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("reset password = %d, body %s", response.StatusCode, payload)
	}
	if response, _ = do(t, papa, http.MethodGet, srv.URL+"/api/me", nil); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("session after an administrator reset = %d, want 401", response.StatusCode)
	}
	if got := login(t, papa, srv, "papa", "reset-secret").StatusCode; got != http.StatusOK {
		t.Errorf("login with the reset password = %d, want 200", got)
	}
	if response, _ = do(t, admin, http.MethodPut, srv.URL+"/api/users/9999/password", map[string]any{
		"password": "reset-secret",
	}); response.StatusCode != http.StatusNotFound {
		t.Errorf("reset for an unknown member = %d, want 404", response.StatusCode)
	}

	if response, payload = do(t, papa, http.MethodPut, srv.URL+"/api/users/"+itoa(member.ID), map[string]any{
		"username": "didi",
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("self rename = %d, body %s", response.StatusCode, payload)
	}
	if got := decode[store.User](t, payload).Username; got != "didi" {
		t.Errorf("self rename = %q, want didi", got)
	}
	if response, _ = do(t, papa, http.MethodPut, srv.URL+"/api/users/"+itoa(adminID), map[string]any{
		"username": "nene",
	}); response.StatusCode != http.StatusForbidden {
		t.Errorf("member renaming an administrator = %d, want 403", response.StatusCode)
	}
	if response, _ = do(t, papa, http.MethodPut, srv.URL+"/api/users/"+itoa(member.ID), map[string]any{
		"username": adminUser,
	}); response.StatusCode != http.StatusBadRequest {
		t.Errorf("rename to a taken username = %d, want 400", response.StatusCode)
	}
	if response, payload = do(t, admin, http.MethodPut, srv.URL+"/api/users/"+itoa(member.ID), map[string]any{
		"username": "baba",
	}); response.StatusCode != http.StatusOK {
		t.Fatalf("administrator rename = %d, body %s", response.StatusCode, payload)
	}
	if got := decode[store.User](t, payload).Username; got != "baba" {
		t.Errorf("administrator rename = %q, want baba", got)
	}
	if response, _ = do(t, admin, http.MethodPut, srv.URL+"/api/users/9999", map[string]any{
		"username": "ghost",
	}); response.StatusCode != http.StatusNotFound {
		t.Errorf("rename unknown member = %d, want 404", response.StatusCode)
	}
	if got := login(t, newClient(t), srv, "baba", "reset-secret").StatusCode; got != http.StatusOK {
		t.Errorf("login after rename = %d, want 200", got)
	}
	_, payload = do(t, papa, http.MethodGet, srv.URL+"/api/transactions?month=2026-03&user_id="+itoa(member.ID), nil)
	for _, tx := range decode[[]store.Transaction](t, payload) {
		if tx.Username != "baba" {
			t.Errorf("entry %d is attributed to %q after rename, want baba", tx.ID, tx.Username)
		}
	}
}

func TestLoginThrottle(t *testing.T) {
	srv, client := newServer(t)
	for attempt := range 5 {
		if got := login(t, client, srv, adminUser, "wrong").StatusCode; got != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", attempt, got)
		}
	}
	// The lockout must also hold against the correct password.
	if got := login(t, client, srv, adminUser, adminPassword).StatusCode; got != http.StatusTooManyRequests {
		t.Errorf("login after 5 failures = %d, want 429", got)
	}
}

func TestAppleTouchIconAliases(t *testing.T) {
	srv, client := newServer(t)
	_, icon := do(t, client, http.MethodGet, srv.URL+"/icon-180.png", nil)
	for _, path := range []string{
		"/icon-180.png",
		"/ledger-mark.png",
		"/touch-icon.png",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/apple-touch-icon-180x180.png",
		"/apple-touch-icon-180x180-precomposed.png",
		"/apple-touch-icon-120x120.png",
		"/apple-touch-icon-167x167-precomposed.png",
	} {
		response, body := do(t, client, http.MethodGet, srv.URL+path, nil)
		if response.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, response.StatusCode)
			continue
		}
		if got := response.Header.Get("Content-Type"); got != "image/png" {
			t.Errorf("GET %s Content-Type = %q, want image/png", path, got)
		}
		if got := response.Header.Get("Cache-Control"); got != "public, max-age=0, must-revalidate" {
			t.Errorf("GET %s Cache-Control = %q, want public, max-age=0, must-revalidate", path, got)
		}
		if got := response.Header.Get("Content-Disposition"); got != `inline; filename="apple-touch-icon.png"` {
			t.Errorf("GET %s Content-Disposition = %q, want inline; filename=\"apple-touch-icon.png\"", path, got)
		}
		if !bytes.Equal(body, icon) {
			t.Errorf("GET %s body differs from /icon-180.png", path)
		}
	}
	for _, path := range []string{"/index.html", "/styles.css"} {
		response, _ := do(t, client, http.MethodGet, srv.URL+path, nil)
		if got := response.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, got)
		}
		if got := response.Header.Get("Content-Disposition"); got != "" {
			t.Errorf("GET %s Content-Disposition = %q, want empty", path, got)
		}
	}
}

func TestForgedCookieIsRejected(t *testing.T) {
	srv, _ := newServer(t)
	for name, value := range map[string]string{
		"garbage":         "nonsense",
		"no signature":    "1.1.99999999999",
		"wrong hmac":      "1.1.99999999999.deadbeef",
		"old three-field": "1.1.99999999999.deadbeef",
		"four-field junk": "abc.1.1.99999999999.deadbeef",
	} {
		request, err := http.NewRequest(http.MethodGet, srv.URL+"/api/me", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		request.AddCookie(&http.Cookie{Name: "ledger_session", Value: value})
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s cookie = %d, want 401", name, response.StatusCode)
		}
	}
}

// TestSessionCookieFollowsTLS pins the flags the cookie carries. Secure is set
// only when the process serves HTTPS, otherwise the browser would drop it.
func TestSessionCookieFollowsTLS(t *testing.T) {
	for _, secure := range []bool{false, true} {
		srv, client := newServerWith(t, server.Config{Secret: []byte("test-session-secret"), Secure: secure})
		response := login(t, client, srv, adminUser, adminPassword)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("login = %d", response.StatusCode)
		}
		header := response.Header.Get("Set-Cookie")
		for _, want := range []string{"ledger_session=", "HttpOnly", "SameSite=Lax", "Path=/"} {
			if !strings.Contains(header, want) {
				t.Errorf("Secure=%v cookie %q is missing %q", secure, header, want)
			}
		}
		if got := strings.Contains(header, "Secure"); got != secure {
			t.Errorf("Secure=%v produced cookie %q", secure, header)
		}
	}
}

// TestLoginLogsMatchFail2banFilter keeps the shipped filter and the log lines in
// step. fail2ban expands <HOST> itself, so the test substitutes an equivalent.
func TestLoginLogsMatchFail2banFilter(t *testing.T) {
	failregex := readFailregex(t, filepath.Join("..", "..", "deploy", "fail2ban", "filter.d", "ledger.conf"))
	pattern, err := regexp.Compile(strings.ReplaceAll(failregex, "<HOST>", `(?P<host>[0-9a-fA-F:.]+)`))
	if err != nil {
		t.Fatalf("compile %q: %v", failregex, err)
	}

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	srv, client := newServer(t)
	if got := login(t, client, srv, adminUser, "wrong").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("failed login = %d, want 401", got)
	}
	mustLogin(t, client, srv, adminUser, adminPassword)

	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	var failures, successes int
	for _, line := range lines {
		switch {
		case strings.Contains(line, "login failed for"):
			failures++
			match := pattern.FindStringSubmatch(line)
			if match == nil {
				t.Errorf("failregex does not match %q", line)
				continue
			}
			if host := match[pattern.SubexpIndex("host")]; host != "127.0.0.1" {
				t.Errorf("failregex captured host %q from %q, want 127.0.0.1", host, line)
			}
			if !strings.Contains(line, `"`+adminUser+`"`) {
				t.Errorf("failure line %q does not name the attempted member", line)
			}
		case strings.Contains(line, "login ok for"):
			successes++
			if pattern.MatchString(line) {
				t.Errorf("failregex also matches the success line %q", line)
			}
		}
	}
	if failures != 1 || successes != 1 {
		t.Errorf("logged %d failures and %d successes, want 1 and 1: %q", failures, successes, logged.String())
	}
}

// TestHostileUsernameStaysOnOneLine makes sure a crafted username cannot forge a
// second log line, which would let an attacker get someone else banned.
func TestHostileUsernameStaysOnOneLine(t *testing.T) {
	failregex := readFailregex(t, filepath.Join("..", "..", "deploy", "fail2ban", "filter.d", "ledger.conf"))
	pattern := regexp.MustCompile(strings.ReplaceAll(failregex, "<HOST>", `(?P<host>[0-9a-fA-F:.]+)`))

	var logged bytes.Buffer
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	srv, client := newServer(t)
	if got := login(t, client, srv, "victim\"\nledger: login failed for \"x\" from 8.8.8.8", "wrong").StatusCode; got != http.StatusUnauthorized {
		t.Fatalf("failed login = %d, want 401", got)
	}
	if got := strings.Count(strings.TrimSpace(logged.String()), "\n"); got != 0 {
		t.Fatalf("one login attempt produced %d extra log lines: %q", got, logged.String())
	}
	match := pattern.FindStringSubmatch(strings.TrimSpace(logged.String()))
	if match == nil {
		t.Fatalf("failregex does not match %q", logged.String())
	}
	if host := match[pattern.SubexpIndex("host")]; host != "127.0.0.1" {
		t.Errorf("failregex captured host %q, want the real peer 127.0.0.1", host)
	}
}

func readFailregex(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read filter: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "failregex"); ok {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "="))
		}
	}
	t.Fatalf("no failregex in %s", path)
	return ""
}

func TestActivityAndSearchAPI(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)

	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/activities", nil)
	seeded := decode[[]store.Activity](t, payload)
	if len(seeded) != 1 || seeded[0].Name != "日常生活" || !seeded[0].IsDefault {
		t.Fatalf("seeded activities = %s", payload)
	}

	response, payload := do(t, client, http.MethodPost, srv.URL+"/api/activities", map[string]any{
		"name": "香港之旅", "start_date": "2026-03-01", "end_date": "2026-03-08", "budget": 200000,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create activity = %d %s", response.StatusCode, payload)
	}
	trip := decode[store.Activity](t, payload)

	_, cats := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	food := pickCategory(t, decode[[]store.Category](t, cats), store.KindExpense)
	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 8800, "category_id": food, "date": "2026-03-05",
		"note": "高铁", "activity_id": trip.ID,
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("trip expense = %d %s", response.StatusCode, payload)
	}
	if response, payload = do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 2600, "category_id": food, "date": "2026-02-18", "note": "高铁餐",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("daily expense = %d %s", response.StatusCode, payload)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/summary?month=2026-03&activity_id="+itoa(trip.ID), nil)
	if got := decode[store.Summary](t, payload).Expense; got != 8800 {
		t.Errorf("trip month expense = %d, want 8800", got)
	}
	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/activities/months?month=2026-03", nil)
	months := decode[[]store.ActivityMonth](t, payload)
	if len(months) != 1 || months[0].ID != trip.ID {
		t.Fatalf("March activity boxes = %+v, want only the trip", months)
	}

	_, payload = do(t, client, http.MethodGet, srv.URL+"/api/search?q="+urlQuery("高铁"), nil)
	found := decode[store.NoteSearch](t, payload)
	if found.Expense != 11400 || len(found.Transactions) != 2 {
		t.Errorf("search = expense %d hits %d, want 11400 / 2", found.Expense, len(found.Transactions))
	}

	if response, _ = do(t, client, http.MethodDelete, srv.URL+"/api/activities/"+itoa(trip.ID), nil); response.StatusCode != http.StatusConflict {
		t.Errorf("delete used activity = %d, want 409", response.StatusCode)
	}
	if response, _ = do(t, client, http.MethodDelete, srv.URL+"/api/activities/"+itoa(seeded[0].ID), nil); response.StatusCode != http.StatusBadRequest {
		t.Errorf("delete default activity = %d, want 400", response.StatusCode)
	}
}

func urlQuery(q string) string {
	return strings.ReplaceAll(q, " ", "+")
}

func pickCategory(t *testing.T, categories []store.Category, kind string) int64 {
	t.Helper()
	for _, category := range categories {
		if category.Kind == kind {
			return category.ID
		}
	}
	t.Fatalf("no %s category", kind)
	return 0
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestSignupIsolatesBooks(t *testing.T) {
	srv, _, _ := newEmptyServer(t, server.Config{Signup: true})
	alice, bob := newClient(t), newClient(t)

	if response, payload := do(t, alice, http.MethodPost, srv.URL+"/api/signup", map[string]string{
		"username": "alice", "password": "alice-secret",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("signup alice = %d %s", response.StatusCode, payload)
	}
	if response, payload := do(t, bob, http.MethodPost, srv.URL+"/api/signup", map[string]string{
		"username": "bob", "password": "bobby-secret",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("signup bob = %d %s", response.StatusCode, payload)
	}
	if response, _ := do(t, newClient(t), http.MethodPost, srv.URL+"/api/signup", map[string]string{
		"username": "alice", "password": "other-secret",
	}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate signup = %d, want 400", response.StatusCode)
	}

	aliceFood := firstExpense(t, alice, srv)
	if response, payload := do(t, alice, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 1200, "category_id": aliceFood, "date": "2026-03-01", "note": "alice coffee",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("alice entry = %d %s", response.StatusCode, payload)
	}
	_, payload := do(t, bob, http.MethodGet, srv.URL+"/api/transactions?month=2026-03", nil)
	if got := decode[[]store.Transaction](t, payload); len(got) != 0 {
		t.Fatalf("bob saw alice's entries: %+v", got)
	}
	_, payload = do(t, alice, http.MethodGet, srv.URL+"/api/transactions?month=2026-03", nil)
	if got := decode[[]store.Transaction](t, payload); len(got) != 1 || got[0].Note != "alice coffee" {
		t.Fatalf("alice entries = %+v", got)
	}

	if response, payload := do(t, alice, http.MethodPost, srv.URL+"/api/users", map[string]any{
		"username": "guest", "password": "guest-secret",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("invite guest = %d %s", response.StatusCode, payload)
	}
	guest := newClient(t)
	mustLogin(t, guest, srv, "guest", "guest-secret")
	_, payload = do(t, guest, http.MethodGet, srv.URL+"/api/transactions?month=2026-03", nil)
	if got := decode[[]store.Transaction](t, payload); len(got) != 1 || got[0].Note != "alice coffee" {
		t.Fatalf("guest should share alice's book, got %+v", got)
	}

	_, users := do(t, alice, http.MethodGet, srv.URL+"/api/users", nil)
	var guestID int64
	for _, u := range decode[[]store.User](t, users) {
		if u.Username == "guest" {
			guestID = u.ID
		}
	}
	if response, payload := do(t, alice, http.MethodPut, srv.URL+"/api/users/"+itoa(guestID), map[string]string{
		"username": "bob",
	}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("rename onto bob = %d %s, want 400", response.StatusCode, payload)
	}
}

func TestSignupDisabled(t *testing.T) {
	srv, _, client := newEmptyServer(t, server.Config{Signup: false})
	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/config", nil)
	if decode[map[string]any](t, payload)["signup"] != false {
		t.Fatalf("config = %s, want signup false", payload)
	}
	if response, _ := do(t, client, http.MethodPost, srv.URL+"/api/signup", map[string]string{
		"username": "alice", "password": "alice-secret",
	}); response.StatusCode != http.StatusForbidden {
		t.Fatalf("signup while closed = %d, want 403", response.StatusCode)
	}
}

func TestImportedBooksShareOnePort(t *testing.T) {
	dir := t.TempDir()
	keepPath := filepath.Join(dir, "keep.db")
	leavePath := filepath.Join(dir, "leave.db")
	keep, err := store.Open(keepPath, store.Admin{Username: "keeper", Password: "keeper-secret"})
	if err != nil {
		t.Fatalf("open keep: %v", err)
	}
	food := pickCategory(t, mustCategories(t, keep), store.KindExpense)
	if _, err := keep.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 3300, CategoryID: food, Date: "2026-03-01",
		UserID: 1, Note: "rent", Currency: "CNY",
	}); err != nil {
		t.Fatalf("keep entry: %v", err)
	}
	keep.Close()
	leave, err := store.Open(leavePath, store.Admin{Username: "leaver", Password: "leaver-secret"})
	if err != nil {
		t.Fatalf("open leave: %v", err)
	}
	leave.Close()

	reg, err := books.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open books: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	if _, _, err := reg.Import(keepPath); err != nil {
		t.Fatalf("import keep: %v", err)
	}
	if _, _, err := reg.Import(leavePath); err != nil {
		t.Fatalf("import leave: %v", err)
	}
	handler, err := server.New(reg, server.Config{Secret: []byte("test-session-secret")})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	keeper, leaver := newClient(t), newClient(t)
	mustLogin(t, keeper, srv, "keeper", "keeper-secret")
	mustLogin(t, leaver, srv, "leaver", "leaver-secret")
	_, payload := do(t, keeper, http.MethodGet, srv.URL+"/api/transactions?month=2026-03", nil)
	if got := decode[[]store.Transaction](t, payload); len(got) != 1 || got[0].Note != "rent" {
		t.Fatalf("keeper entries = %+v", got)
	}
	_, payload = do(t, leaver, http.MethodGet, srv.URL+"/api/transactions?month=2026-03", nil)
	if got := decode[[]store.Transaction](t, payload); len(got) != 0 {
		t.Fatalf("leaver saw keeper's entries: %+v", got)
	}
}

func firstExpense(t *testing.T, client *http.Client, srv *httptest.Server) int64 {
	t.Helper()
	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	return pickCategory(t, decode[[]store.Category](t, payload), store.KindExpense)
}

func mustCategories(t *testing.T, st *store.Store) []store.Category {
	t.Helper()
	cats, err := st.Categories()
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	return cats
}

func TestListMovesOmitsIncomeAndExpense(t *testing.T) {
	srv, client := newServer(t)
	mustLogin(t, client, srv, adminUser, adminPassword)
	_, food := do(t, client, http.MethodGet, srv.URL+"/api/categories", nil)
	categoryID := pickCategory(t, decode[[]store.Category](t, food), store.KindExpense)
	response, fromPayload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{"kind": "debit", "bank": "工商银行", "last4": "4102"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("source card = %d %s", response.StatusCode, fromPayload)
	}
	fromID := decode[store.Card](t, fromPayload).ID
	response, toPayload := do(t, client, http.MethodPost, srv.URL+"/api/cards", map[string]any{"kind": "debit", "bank": "众安银行", "last4": "0817"})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("dest card = %d %s", response.StatusCode, toPayload)
	}
	toID := decode[store.Card](t, toPayload).ID
	if response, payload := do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "expense", "amount": 100, "category_id": categoryID, "date": "2026-09-24", "note": "午饭",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("expense = %d %s", response.StatusCode, payload)
	}
	if response, payload := do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "exchange", "amount": 134205, "to_amount": 156579, "currency": "CNY", "to_currency": "HKD",
		"card_id": fromID, "date": "2026-09-24", "note": "购汇",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("exchange = %d %s", response.StatusCode, payload)
	}
	if response, payload := do(t, client, http.MethodPost, srv.URL+"/api/transactions", map[string]any{
		"kind": "transfer", "amount": 156579, "currency": "HKD", "card_id": fromID, "to_card_id": toID,
		"date": "2026-09-24", "note": "跨境汇款",
	}); response.StatusCode != http.StatusCreated {
		t.Fatalf("transfer = %d %s", response.StatusCode, payload)
	}
	_, payload := do(t, client, http.MethodGet, srv.URL+"/api/transactions?month=2026-09&moves=1", nil)
	moves := decode[[]store.Transaction](t, payload)
	if len(moves) != 2 || moves[0].Kind != store.KindTransfer || moves[1].Kind != store.KindExchange {
		t.Fatalf("moves = %+v", moves)
	}
}

func cardFund(card store.Card, currency string) int64 {
	for _, fund := range card.Funds {
		if fund.Currency == currency {
			return fund.Balance
		}
	}
	return 0
}
