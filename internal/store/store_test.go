package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ledger/internal/store"
)

// testAdmin bootstraps every temporary ledger with one administrator.
var testAdmin = store.Admin{Username: "mama", Password: "family-secret"}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ledger.db"), testAdmin)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// adminID is the recording member used by tests that only need one.
func adminID(t *testing.T, st *store.Store) int64 {
	t.Helper()
	users, err := st.Users()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 || !users[0].IsAdmin || users[0].Username != testAdmin.Username {
		t.Fatalf("bootstrap produced %+v, want one administrator named %q", users, testAdmin.Username)
	}
	return users[0].ID
}

func categoryID(t *testing.T, st *store.Store, kind, name string) int64 {
	t.Helper()
	categories, err := st.Categories()
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	for _, category := range categories {
		if category.Kind == kind && category.Name == name {
			return category.ID
		}
	}
	t.Fatalf("category %s/%q not seeded", kind, name)
	return 0
}

func TestOpenSeedsDefaults(t *testing.T) {
	st := newStore(t)
	categories, err := st.Categories()
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 15 {
		t.Errorf("seeded %d categories, want 15", len(categories))
	}
	expenses := 0
	for _, category := range categories {
		if category.Kind == store.KindExpense {
			expenses++
		}
	}
	if expenses != 10 {
		t.Errorf("seeded %d expense categories, want 10", expenses)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	first, err := store.Open(path, testAdmin)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if first.CreatedAdmin() != testAdmin.Username {
		t.Errorf("first open created %q, want %q", first.CreatedAdmin(), testAdmin.Username)
	}
	first.Close()

	// Reopening must not seed again, and the bootstrap values are then ignored.
	second, err := store.Open(path, store.Admin{Username: "someone-else", Password: "another-secret"})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	if second.CreatedAdmin() != "" {
		t.Errorf("second open created %q, want no new administrator", second.CreatedAdmin())
	}
	categories, err := second.Categories()
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	if len(categories) != 15 {
		t.Errorf("reopen produced %d categories, want 15", len(categories))
	}
	users, err := second.Users()
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) != 1 || users[0].Username != testAdmin.Username {
		t.Errorf("reopen produced users %+v, want only %q", users, testAdmin.Username)
	}
}

func TestOpenWithoutAdminPasswordFails(t *testing.T) {
	_, err := store.Open(filepath.Join(t.TempDir(), "ledger.db"), store.Admin{Username: "admin"})
	if !errors.Is(err, store.ErrAdminRequired) {
		t.Fatalf("open without an administrator password = %v, want ErrAdminRequired", err)
	}
}

func TestSummaryAndList(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	salary := categoryID(t, st, store.KindIncome, "工资")

	entries := []store.TxInput{
		{Kind: store.KindExpense, Amount: 1250, CategoryID: food, Date: "2026-03-04", Note: "午饭"},
		{Kind: store.KindExpense, Amount: 3000, CategoryID: food, Date: "2026-03-04"},
		{Kind: store.KindIncome, Amount: 800000, CategoryID: salary, Date: "2026-03-10"},
		{Kind: store.KindExpense, Amount: 999, CategoryID: food, Date: "2026-04-01"},
	}
	for i, entry := range entries {
		entry.UserID = admin
		if _, err := st.CreateTransaction(entry); err != nil {
			t.Fatalf("create entry %d: %v", i, err)
		}
	}

	summary, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Expense != 4250 || summary.Income != 800000 {
		t.Errorf("summary totals = %d/%d, want 4250/800000", summary.Expense, summary.Income)
	}
	if len(summary.ExpenseByCategory) != 1 || summary.ExpenseByCategory[0].Amount != 4250 {
		t.Errorf("expense breakdown = %+v, want one 餐饮 row of 4250", summary.ExpenseByCategory)
	}
	if summary.ExpenseByCategory[0].Icon == "" || summary.ExpenseByCategory[0].Color == "" {
		t.Error("expense breakdown is missing icon or colour")
	}
	// April is a different month, so only two days of March are covered.
	if len(summary.Days) != 2 {
		t.Fatalf("summary covers %d days, want 2: %+v", len(summary.Days), summary.Days)
	}
	if summary.Days[0].Date != "2026-03-10" || summary.Days[1].Date != "2026-03-04" {
		t.Errorf("days are not newest first: %+v", summary.Days)
	}
	if summary.Days[1].Expense != 4250 {
		t.Errorf("03-04 expense = %d, want 4250", summary.Days[1].Expense)
	}

	year, err := st.SummaryYear("2026", 0, false)
	if err != nil {
		t.Fatalf("year summary: %v", err)
	}
	if year.Expense != 5249 || year.Income != 800000 {
		t.Errorf("year totals = %d/%d, want 5249/800000", year.Expense, year.Income)
	}
	if len(year.Days) != 0 {
		t.Errorf("year summary returned %d days, want none", len(year.Days))
	}
	points, err := st.TrendYear("2026", 0, false)
	if err != nil {
		t.Fatalf("year trend: %v", err)
	}
	if len(points) != 12 || points[0].Month != "2026-01" || points[11].Month != "2026-12" {
		t.Fatalf("year trend = %+v, want 12 months of 2026", points)
	}
	if points[2].Expense != 4250 || points[3].Expense != 999 {
		t.Errorf("year trend Mar/Apr = %d/%d, want 4250/999", points[2].Expense, points[3].Expense)
	}

	list, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("listed %d entries for 2026-03, want 3", len(list))
	}
	filtered, err := st.Transactions(store.TxFilter{Month: "2026-03", Query: "午饭"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Note != "午饭" {
		t.Errorf("note search returned %+v, want the 午饭 entry", filtered)
	}
	byKind, err := st.Transactions(store.TxFilter{Month: "2026-03", Kind: store.KindIncome})
	if err != nil {
		t.Fatalf("filter by kind: %v", err)
	}
	if len(byKind) != 1 || byKind[0].CategoryName != "工资" {
		t.Errorf("income-only list = %+v, want the 工资 entry", byKind)
	}

	recent, err := st.RecentTransactions(1)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("recent returned %d rows, want 1", len(recent))
	}
}

func TestUpdateAndDeleteTransaction(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	transport := categoryID(t, st, store.KindExpense, "交通")

	created, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 1000, CategoryID: food, Date: "2026-03-04", UserID: admin})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// An edit does not carry a recorder: the original one is kept.
	updated, err := st.UpdateTransaction(created.ID, store.TxInput{
		Kind: store.KindExpense, Amount: 2200, CategoryID: transport, Date: "2026-03-05", Note: "地铁"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Amount != 2200 || updated.CategoryName != "交通" || updated.Note != "地铁" {
		t.Errorf("update produced %+v", updated)
	}
	if updated.UserID != admin || updated.Username != testAdmin.Username {
		t.Errorf("update changed the recorder to %d/%q", updated.UserID, updated.Username)
	}

	if err := st.DeleteTransaction(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.DeleteTransaction(created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateTransaction(created.ID, store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-05"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update after delete = %v, want ErrNotFound", err)
	}
}

func TestTransactionValidation(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	salary := categoryID(t, st, store.KindIncome, "工资")
	missing := int64(9999)

	cases := map[string]store.TxInput{
		"unknown kind":           {Kind: "gift", Amount: 100, CategoryID: food, Date: "2026-03-04"},
		"transfer without cards": {Kind: store.KindTransfer, Amount: 100, Date: "2026-03-04"},
		"zero amount":            {Kind: store.KindExpense, Amount: 0, CategoryID: food, Date: "2026-03-04"},
		"negative amount":        {Kind: store.KindExpense, Amount: -100, CategoryID: food, Date: "2026-03-04"},
		"bad date":               {Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-3-4"},
		"impossible date":        {Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-02-30"},
		"missing category":       {Kind: store.KindExpense, Amount: 100, Date: "2026-03-04"},
		"unknown category":       {Kind: store.KindExpense, Amount: 100, CategoryID: missing, Date: "2026-03-04"},
		"kind mismatch":          {Kind: store.KindExpense, Amount: 100, CategoryID: salary, Date: "2026-03-04"},
		"unknown currency":       {Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-04", Currency: "XXX"},
		"exchange without card":  {Kind: store.KindExchange, Amount: 100, ToAmount: 110, Date: "2026-03-04", Currency: "CNY", ToCurrency: "HKD"},
		"exchange same currency": {Kind: store.KindExchange, Amount: 100, ToAmount: 110, Date: "2026-03-04", Currency: "USD", ToCurrency: "USD"},
	}
	for name, input := range cases {
		input.UserID = admin
		if _, err := st.CreateTransaction(input); err == nil {
			t.Errorf("%s: create succeeded, want rejection", name)
		} else {
			var invalid store.InvalidError
			if !errors.As(err, &invalid) {
				t.Errorf("%s: got %T (%v), want InvalidError", name, err, err)
			}
		}
	}

	// An entry always names an existing recorder.
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-04", UserID: missing,
	}); err == nil {
		t.Error("an unknown recorder was accepted")
	}
}

func TestCategoryLifecycle(t *testing.T) {
	st := newStore(t)
	category, err := st.CreateCategory(store.Category{Name: "咖啡", Kind: store.KindExpense, Icon: "☕", Color: "#4a9ef7"})
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if _, err := st.CreateCategory(store.Category{Name: "咖啡", Kind: store.KindExpense}); err == nil {
		t.Error("duplicate category name within a kind was accepted")
	}
	if _, err := st.CreateCategory(store.Category{Name: "咖啡", Kind: store.KindIncome}); err != nil {
		t.Errorf("same name in the other kind should be allowed: %v", err)
	}
	if _, err := st.CreateCategory(store.Category{Name: "错误", Kind: "saving"}); err == nil {
		t.Error("unknown category kind was accepted")
	}
	if _, err := st.CreateCategory(store.Category{Name: "错误", Kind: store.KindExpense, Color: "blue"}); err == nil {
		t.Error("invalid colour was accepted")
	}
	if err := st.DeleteCategory(category.ID); err != nil {
		t.Fatalf("delete unused category: %v", err)
	}
}

func TestDeleteRefusedWhenInUse(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-04", UserID: admin}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if err := st.DeleteCategory(food); !errors.Is(err, store.ErrInUse) {
		t.Errorf("delete used category = %v, want ErrInUse", err)
	}

	// Archiving stays available as the alternative to deleting.
	if _, err := st.UpdateCategory(food, store.Category{Name: "餐饮", Icon: "🍜", Color: "#ff8a5c", Archived: true}); err != nil {
		t.Fatalf("archive category: %v", err)
	}
}

func TestReorderCategories(t *testing.T) {
	st := newStore(t)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	transport := categoryID(t, st, store.KindExpense, "交通")
	listed, err := st.Categories()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := []int64{}
	for _, category := range listed {
		if category.Kind == store.KindExpense && !category.Archived {
			ids = append(ids, category.ID)
		}
	}
	if len(ids) < 2 || ids[0] != food || ids[1] != transport {
		t.Fatalf("seeded expense order = %v, want 餐饮 then 交通", ids)
	}

	ids[0], ids[1] = ids[1], ids[0]
	if err := st.ReorderCategories(store.KindExpense, ids); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	listed, err = st.Categories()
	if err != nil {
		t.Fatalf("list after reorder: %v", err)
	}
	active := []int64{}
	for _, category := range listed {
		if category.Kind == store.KindExpense && !category.Archived {
			active = append(active, category.ID)
		}
	}
	if active[0] != transport || active[1] != food {
		t.Errorf("reordered expenses = %v, want 交通 then 餐饮", active)
	}

	if err := st.ReorderCategories(store.KindExpense, []int64{food}); err == nil {
		t.Error("a partial order was accepted")
	}
	if err := st.ReorderCategories("saving", ids); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

func TestReorderCards(t *testing.T) {
	st := newStore(t)
	first, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "1234"})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := st.CreateCard(store.Card{Kind: store.CardCredit, Bank: "中信", Last4: "8888"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	listed, err := st.Cards()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != second.ID {
		t.Fatalf("seeded card order = %v, want 招商 then 中信", listed)
	}
	if err := st.ReorderCards([]int64{second.ID, first.ID}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	listed, err = st.Cards()
	if err != nil {
		t.Fatalf("list after reorder: %v", err)
	}
	if listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Errorf("reordered cards = %d then %d, want 中信 then 招商", listed[0].ID, listed[1].ID)
	}
	if err := st.ReorderCards([]int64{first.ID}); err == nil {
		t.Error("a partial order was accepted")
	}
}

func TestBudgetAndTrend(t *testing.T) {
	st := newStore(t)
	if got, err := st.Budget(); err != nil || got != 0 {
		t.Fatalf("default budget = %d (%v), want 0", got, err)
	}
	if err := st.SetBudget(300000); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	if err := st.SetBudget(250000); err != nil {
		t.Fatalf("update budget: %v", err)
	}
	if got, _ := st.Budget(); got != 250000 {
		t.Errorf("budget = %d, want 250000", got)
	}
	if err := st.SetBudget(-1); err == nil {
		t.Error("negative budget was accepted")
	}
	summary, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if summary.Budget != 250000 {
		t.Errorf("summary budget = %d, want 250000", summary.Budget)
	}

	trend, err := st.Trend(12, 0, false)
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend) != 12 {
		t.Fatalf("trend has %d points, want 12", len(trend))
	}
	if trend[0].Month >= trend[11].Month {
		t.Errorf("trend is not oldest first: %s .. %s", trend[0].Month, trend[11].Month)
	}
	if _, err := st.Trend(0, 0, false); err == nil {
		t.Error("Trend(0) was accepted")
	}
	if _, err := st.Summary("2026-13", 0, false); err == nil {
		t.Error("month 2026-13 was accepted")
	}
	if _, err := st.SummaryYear("26", 0, false); err == nil {
		t.Error("year 26 was accepted")
	}
	if _, err := st.TrendYear("2026-03", 0, false); err == nil {
		t.Error("TrendYear(2026-03) was accepted")
	}
}

func TestSummaryAllAndTrendAll(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	trip, err := st.CreateActivity(store.Activity{Name: "香港之旅"})
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 1000, CategoryID: food, Date: "2025-12-20", UserID: admin, Note: "去年",
	}); err != nil {
		t.Fatalf("2025: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 2000, CategoryID: food, Date: "2026-03-05",
		UserID: admin, ActivityID: trip.ID, Note: "今年",
	}); err != nil {
		t.Fatalf("2026: %v", err)
	}

	all, err := st.SummaryAll(0, false, 0)
	if err != nil {
		t.Fatalf("summary all: %v", err)
	}
	if all.Expense != 3000 {
		t.Errorf("all expense = %d, want 3000", all.Expense)
	}
	tripSum, err := st.SummaryAll(0, false, trip.ID)
	if err != nil {
		t.Fatalf("summary trip: %v", err)
	}
	if tripSum.Expense != 2000 {
		t.Errorf("trip all expense = %d, want 2000", tripSum.Expense)
	}

	points, err := st.TrendAll(0, false, 0)
	if err != nil {
		t.Fatalf("trend all: %v", err)
	}
	if len(points) < 2 || points[0].Month != "2025" {
		t.Fatalf("trend years = %+v, want to start at 2025", points)
	}
	if points[0].Expense != 1000 {
		t.Errorf("2025 expense = %d, want 1000", points[0].Expense)
	}
	var year2026 store.TrendPoint
	for _, point := range points {
		if point.Month == "2026" {
			year2026 = point
		}
	}
	if year2026.Expense != 2000 {
		t.Errorf("2026 expense = %d, want 2000", year2026.Expense)
	}
}

func TestBalance(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	salary := categoryID(t, st, store.KindIncome, "工资")

	if got, err := st.FamilyBalance(); err != nil || got != 0 {
		t.Fatalf("default balance = %d (%v), want 0", got, err)
	}

	debit, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "1234", Balance: 20000})
	if err != nil {
		t.Fatalf("create debit: %v", err)
	}
	credit, err := st.CreateCard(store.Card{Kind: store.CardCredit, Bank: "中信", Last4: "8888", Balance: 3000})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}

	for i, entry := range []store.TxInput{
		{Kind: store.KindExpense, Amount: 3000, CategoryID: food, Date: "2026-03-04", UserID: admin, CardID: debit.ID},
		{Kind: store.KindExpense, Amount: 2000, CategoryID: food, Date: "2026-03-04", UserID: admin, Shared: true},
		{Kind: store.KindIncome, Amount: 8000, CategoryID: salary, Date: "2026-04-01", UserID: admin, CardID: debit.ID},
		{Kind: store.KindTransfer, Amount: 1000, Date: "2026-03-04", UserID: admin, CardID: debit.ID, ToCardID: credit.ID},
	} {
		if _, err := st.CreateTransaction(entry); err != nil {
			t.Fatalf("create entry %d: %v", i, err)
		}
	}

	if got, err := st.FamilyBalance(); err != nil || got != 28000 {
		t.Fatalf("family balance = %d (%v), want 28000", got, err)
	}

	march, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("family March: %v", err)
	}
	if march.Balance != 28000 {
		t.Errorf("family March balance = %d, want 28000", march.Balance)
	}
	if march.Income != 0 || march.Expense != 5000 {
		t.Errorf("family March totals = %d/%d, want 0/5000", march.Income, march.Expense)
	}

	personal, err := st.Summary("2026-03", admin, false)
	if err != nil {
		t.Fatalf("personal: %v", err)
	}
	if personal.Balance != 5000 {
		t.Errorf("personal balance = %d, want 5000", personal.Balance)
	}

	shared, err := st.Summary("2026-03", 0, true)
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	if shared.Balance != -2000 {
		t.Errorf("shared balance = %d, want -2000", shared.Balance)
	}

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 1500, CategoryID: food, Date: "2026-04-02", UserID: admin, CardID: debit.ID,
	}); err != nil {
		t.Fatalf("later expense: %v", err)
	}
	if got, err := st.FamilyBalance(); err != nil || got != 26500 {
		t.Fatalf("after later card expense = %d (%v), want 26500", got, err)
	}
}

func TestSharedExpense(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	created, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 8800, CategoryID: food, Date: "2026-03-04",
		Note: "聚餐", UserID: admin, Shared: true,
	})
	if err != nil {
		t.Fatalf("create shared: %v", err)
	}
	if !created.Shared {
		t.Fatal("created expense is not shared")
	}

	listed, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || !listed[0].Shared {
		t.Fatalf("list = %+v, want one shared row", listed)
	}

	updated, err := st.UpdateTransaction(created.ID, store.TxInput{
		Kind: store.KindExpense, Amount: 8800, CategoryID: food, Date: "2026-03-04",
		Note: "聚餐", Shared: false,
	})
	if err != nil {
		t.Fatalf("clear shared: %v", err)
	}
	if updated.Shared {
		t.Error("update left the expense shared")
	}
}

func TestSharedSummary(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	transport := categoryID(t, st, store.KindExpense, "交通")
	salary := categoryID(t, st, store.KindIncome, "工资")

	for i, entry := range []store.TxInput{
		{Kind: store.KindExpense, Amount: 1000, CategoryID: food, Date: "2026-03-04", UserID: admin},
		{Kind: store.KindExpense, Amount: 2500, CategoryID: food, Date: "2026-03-04", UserID: admin, Shared: true},
		{Kind: store.KindExpense, Amount: 800, CategoryID: transport, Date: "2026-03-10", UserID: admin, Shared: true},
		{Kind: store.KindIncome, Amount: 50000, CategoryID: salary, Date: "2026-03-10", UserID: admin, Shared: true},
	} {
		if _, err := st.CreateTransaction(entry); err != nil {
			t.Fatalf("create entry %d: %v", i, err)
		}
	}

	whole, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("family summary: %v", err)
	}
	if whole.Expense != 4300 {
		t.Errorf("family expense = %d, want 4300", whole.Expense)
	}

	shared, err := st.Summary("2026-03", 0, true)
	if err != nil {
		t.Fatalf("shared summary: %v", err)
	}
	if shared.Expense != 3300 || shared.Income != 50000 {
		t.Errorf("shared totals = %d/%d, want 3300/50000", shared.Expense, shared.Income)
	}
	if len(shared.ExpenseByCategory) != 2 {
		t.Fatalf("shared breakdown = %+v, want two categories", shared.ExpenseByCategory)
	}
	if shared.ExpenseByCategory[0].Name != "餐饮" || shared.ExpenseByCategory[0].Amount != 2500 {
		t.Errorf("first shared slice = %+v, want 餐饮 2500", shared.ExpenseByCategory[0])
	}

	trend, err := st.Trend(12, 0, true)
	if err != nil {
		t.Fatalf("shared trend: %v", err)
	}
	var march store.TrendPoint
	for _, point := range trend {
		if point.Month == "2026-03" {
			march = point
		}
	}
	if march.Expense != 3300 || march.Income != 50000 {
		t.Errorf("shared March trend = %+v, want 3300/50000", march)
	}

	personal, err := st.Summary("2026-03", admin, false)
	if err != nil {
		t.Fatalf("personal summary: %v", err)
	}
	if personal.Expense != 1000 || personal.Income != 0 {
		t.Errorf("personal totals = %d/%d, want 1000/0", personal.Expense, personal.Income)
	}

	mine, err := st.Transactions(store.TxFilter{Month: "2026-03", UserID: admin})
	if err != nil {
		t.Fatalf("personal list: %v", err)
	}
	if len(mine) != 1 || mine[0].Shared || mine[0].Amount != 1000 {
		t.Fatalf("personal list = %+v, want one unmarked 1000", mine)
	}

	joint, err := st.Transactions(store.TxFilter{Month: "2026-03", SharedOnly: true})
	if err != nil {
		t.Fatalf("shared list: %v", err)
	}
	if len(joint) != 3 {
		t.Fatalf("shared list has %d entries, want 3", len(joint))
	}
}

func TestMigrateV1AddsShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (1)`,
		`CREATE TABLE users (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  username TEXT NOT NULL UNIQUE,
		  password_hash TEXT NOT NULL,
		  is_admin INTEGER NOT NULL DEFAULT 0,
		  token_version INTEGER NOT NULL DEFAULT 1,
		  created_at TEXT NOT NULL)`,
		`CREATE TABLE categories (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  name TEXT NOT NULL,
		  kind TEXT NOT NULL,
		  icon TEXT NOT NULL DEFAULT '',
		  color TEXT NOT NULL DEFAULT '',
		  sort_order INTEGER NOT NULL DEFAULT 0,
		  archived INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE transactions (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind TEXT NOT NULL,
		  amount INTEGER NOT NULL,
		  category_id INTEGER NOT NULL,
		  user_id INTEGER NOT NULL,
		  date TEXT NOT NULL,
		  note TEXT NOT NULL DEFAULT '',
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL)`,
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at)
		 VALUES ('mama', 'x', 1, 1, '2026-03-04T00:00:00Z')`,
		`INSERT INTO categories (name, kind, icon, color, sort_order) VALUES ('餐饮', 'expense', '', '', 1)`,
		`INSERT INTO transactions (kind, amount, category_id, user_id, date, note, created_at, updated_at)
		 VALUES ('expense', 1000, 1, 1, '2026-03-04', '午饭', '2026-03-04T00:00:00Z', '2026-03-04T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v1: %v\n%s", err, statement)
		}
	}
	db.Close()

	st, err := store.Open(path, testAdmin)
	if err != nil {
		t.Fatalf("open v1 database: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	listed, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list after migrate: %v", err)
	}
	if len(listed) != 1 || listed[0].Note != "午饭" || listed[0].Shared {
		t.Fatalf("migrated row = %+v, want the old 午饭 expense unmarked", listed)
	}

	created, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 2000, CategoryID: listed[0].CategoryID,
		Date: "2026-03-05", UserID: listed[0].UserID, Shared: true,
	})
	if err != nil {
		t.Fatalf("create shared after migrate: %v", err)
	}
	if !created.Shared {
		t.Error("new expense after migrate is not shared")
	}

	debit, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "1234", Balance: 8000})
	if err != nil {
		t.Fatalf("create card after v1 migrate: %v", err)
	}
	if debit.Balance != 8000 {
		t.Errorf("migrated ledger card balance = %d, want 8000", debit.Balance)
	}
}

func TestCardsAndTransfers(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	salary := categoryID(t, st, store.KindIncome, "工资")

	debit, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "汇丰香港", Last4: "1234", Name: "Pulse", Network: store.NetworkVisa, Balance: 2000000})
	if err != nil {
		t.Fatalf("create debit: %v", err)
	}
	credit, err := st.CreateCard(store.Card{Kind: store.CardCredit, Bank: "中信", Last4: "8888", Balance: 0})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}
	if debit.Kind != store.CardDebit || debit.Name != "Pulse" || debit.Network != store.NetworkVisa || credit.Last4 != "8888" {
		t.Fatalf("cards = %+v / %+v", debit, credit)
	}
	if _, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "3333", Network: "foo"}); err == nil {
		t.Error("unknown card network was accepted")
	}
	if _, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Name: strings.Repeat("x", 25), Last4: "1111"}); err == nil {
		t.Error("overlong card name was accepted")
	}

	if _, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "12"}); err == nil {
		t.Error("short last4 was accepted")
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindTransfer, Amount: 100, Date: "2026-03-04", UserID: admin,
		CardID: debit.ID, ToCardID: debit.ID,
	}); err == nil {
		t.Error("same-card transfer was accepted")
	}

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 3500, CategoryID: food, Date: "2026-03-04",
		UserID: admin, CardID: debit.ID,
	}); err != nil {
		t.Fatalf("debit expense: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindIncome, Amount: 8000, CategoryID: salary, Date: "2026-03-04",
		UserID: admin, CardID: debit.ID,
	}); err != nil {
		t.Fatalf("debit income: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindTransfer, Amount: 5000, Date: "2026-03-04", UserID: admin,
		CardID: debit.ID, ToCardID: credit.ID, Note: "还款",
	}); err != nil {
		t.Fatalf("transfer: %v", err)
	}

	got, err := st.Card(debit.ID)
	if err != nil {
		t.Fatalf("reload debit: %v", err)
	}
	if got.Balance != 1999500 {
		t.Errorf("debit after flow = %d, want 1999500", got.Balance)
	}
	got, err = st.Card(credit.ID)
	if err != nil {
		t.Fatalf("reload credit: %v", err)
	}
	if got.Balance != 5000 {
		t.Errorf("credit after transfer = %d, want 5000", got.Balance)
	}

	march, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if march.Income != 8000 || march.Expense != 3500 {
		t.Errorf("family totals = %d/%d, want 8000/3500 (transfer excluded)", march.Income, march.Expense)
	}
	if march.Balance != 2004500 {
		t.Errorf("family balance = %d, want 2004500", march.Balance)
	}
	if len(march.ExpenseByCard) != 1 || march.ExpenseByCard[0].CardID != debit.ID || march.ExpenseByCard[0].Amount != 3500 {
		t.Errorf("expense by card = %+v, want 汇丰香港 3500", march.ExpenseByCard)
	}
	if len(march.IncomeByCard) != 1 || march.IncomeByCard[0].CardID != debit.ID || march.IncomeByCard[0].Amount != 8000 {
		t.Errorf("income by card = %+v, want 汇丰香港 8000", march.IncomeByCard)
	}

	if err := st.SetCardBalance(debit.ID, store.CurrencyCNY, 10000); err != nil {
		t.Fatalf("set debit balance: %v", err)
	}
	got, err = st.Card(debit.ID)
	if err != nil {
		t.Fatalf("reload after set: %v", err)
	}
	if got.Balance != 10000 {
		t.Errorf("set debit balance = %d, want 10000", got.Balance)
	}

	if err := st.DeleteCard(debit.ID); !errors.Is(err, store.ErrInUse) {
		t.Errorf("delete used card = %v, want ErrInUse", err)
	}
}

func TestMigrateV2AddsCards(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (2)`,
		`CREATE TABLE users (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  username TEXT NOT NULL UNIQUE,
		  password_hash TEXT NOT NULL,
		  is_admin INTEGER NOT NULL DEFAULT 0,
		  token_version INTEGER NOT NULL DEFAULT 1,
		  created_at TEXT NOT NULL)`,
		`CREATE TABLE categories (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  name TEXT NOT NULL,
		  kind TEXT NOT NULL,
		  icon TEXT NOT NULL DEFAULT '',
		  color TEXT NOT NULL DEFAULT '',
		  sort_order INTEGER NOT NULL DEFAULT 0,
		  archived INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE transactions (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind TEXT NOT NULL CHECK (kind IN ('expense', 'income')),
		  amount INTEGER NOT NULL,
		  category_id INTEGER NOT NULL,
		  user_id INTEGER NOT NULL,
		  date TEXT NOT NULL,
		  note TEXT NOT NULL DEFAULT '',
		  shared INTEGER NOT NULL DEFAULT 0,
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at)
		 VALUES ('mama', 'x', 1, 1, '2026-03-04T00:00:00Z')`,
		`INSERT INTO categories (name, kind, icon, color, sort_order) VALUES ('餐饮', 'expense', '', '', 1)`,
		`INSERT INTO transactions (kind, amount, category_id, user_id, date, note, shared, created_at, updated_at)
		 VALUES ('expense', 1000, 1, 1, '2026-03-04', '午饭', 0, '2026-03-04T00:00:00Z', '2026-03-04T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v2: %v\n%s", err, statement)
		}
	}
	db.Close()

	st, err := store.Open(path, testAdmin)
	if err != nil {
		t.Fatalf("open v2 database: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	listed, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list after migrate: %v", err)
	}
	if len(listed) != 1 || listed[0].Note != "午饭" || listed[0].CardID != 0 {
		t.Fatalf("migrated row = %+v", listed)
	}
	if _, err := st.Cards(); err != nil {
		t.Fatalf("list cards after migrate: %v", err)
	}
}

func TestExchange(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	debit, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "汇丰", Last4: "1234", Balance: 10000})
	if err != nil {
		t.Fatalf("create debit: %v", err)
	}
	credit, err := st.CreateCard(store.Card{Kind: store.CardCredit, Bank: "中信", Last4: "8888"})
	if err != nil {
		t.Fatalf("create credit: %v", err)
	}

	created, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExchange, Amount: 2000, ToAmount: 2200,
		Date: "2026-03-04", UserID: admin, CardID: debit.ID,
		Currency: store.CurrencyCNY, ToCurrency: store.CurrencyHKD, Note: "换港币",
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if created.Kind != store.KindExchange || created.Amount != 2000 || created.ToAmount != 2200 || created.ToCurrency != store.CurrencyHKD {
		t.Fatalf("created exchange = %+v", created)
	}

	got, err := st.Card(debit.ID)
	if err != nil {
		t.Fatalf("reload debit: %v", err)
	}
	if fundOf(got, store.CurrencyCNY) != 8000 || fundOf(got, store.CurrencyHKD) != 2200 {
		t.Errorf("debit funds = %+v, want CNY 8000 HKD 2200", got.Funds)
	}

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindTransfer, Amount: 500, Date: "2026-03-04", UserID: admin,
		CardID: debit.ID, ToCardID: credit.ID, Currency: store.CurrencyHKD,
	}); err != nil {
		t.Fatalf("hkd transfer: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 300, CategoryID: food, Date: "2026-03-04",
		UserID: admin, CardID: debit.ID, Currency: store.CurrencyHKD,
	}); err != nil {
		t.Fatalf("hkd expense: %v", err)
	}

	got, err = st.Card(debit.ID)
	if err != nil {
		t.Fatalf("reload debit after hkd flow: %v", err)
	}
	if fundOf(got, store.CurrencyHKD) != 1400 {
		t.Errorf("debit HKD = %d, want 1400", fundOf(got, store.CurrencyHKD))
	}
	got, err = st.Card(credit.ID)
	if err != nil {
		t.Fatalf("reload credit: %v", err)
	}
	if fundOf(got, store.CurrencyHKD) != 500 {
		t.Errorf("credit HKD = %d, want 500", fundOf(got, store.CurrencyHKD))
	}

	march, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if march.Balance != 8000 {
		t.Errorf("family CNY = %d, want 8000", march.Balance)
	}
	if totalOf(march.Balances, store.CurrencyHKD) != 1900 {
		t.Errorf("family HKD = %d, want 1900 from %+v", totalOf(march.Balances, store.CurrencyHKD), march.Balances)
	}
	if march.Expense != 300 || march.Income != 0 {
		t.Errorf("family totals = %d/%d, want 0/300 (exchange excluded from income)", march.Income, march.Expense)
	}
	if flow := currencyFlowOf(march.Currencies, store.CurrencyHKD); flow.Balance != 1900 || flow.Expense != 300 || flow.Income != 0 {
		t.Errorf("HKD flow = %+v, want balance 1900 expense 300", flow)
	}
	if flow := currencyFlowOf(march.Currencies, store.CurrencyCNY); flow.Balance != 8000 || flow.Expense != 0 {
		t.Errorf("CNY flow = %+v, want balance 8000", flow)
	}

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExchange, Amount: 100, ToAmount: 110, Date: "2026-03-04",
		UserID: admin, CardID: debit.ID, Currency: store.CurrencyUSD, ToCurrency: store.CurrencyUSD,
	}); err == nil {
		t.Error("same-currency exchange was accepted")
	}
	if _, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "2222", Currency: "XXX", Balance: 1}); err == nil {
		t.Error("unknown card currency was accepted")
	}
}

func TestRemoveCardCurrency(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	card, err := st.CreateCard(store.Card{
		Kind: store.CardDebit, Bank: "汇丰", Last4: "1234",
		Funds: []store.CardFund{
			{Currency: store.CurrencyCNY, Balance: 10000},
			{Currency: store.CurrencyHKD, Balance: 5000},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if fundOf(card, store.CurrencyHKD) != 5000 {
		t.Fatalf("created funds = %+v", card.Funds)
	}

	got, err := st.UpdateCard(card.ID, store.Card{
		Kind: store.CardDebit, Bank: "汇丰", Last4: "1234",
		Funds: []store.CardFund{{Currency: store.CurrencyCNY, Balance: 10000}},
	}, nil)
	if err != nil {
		t.Fatalf("drop unused HKD: %v", err)
	}
	if len(got.Funds) != 1 || fundOf(got, store.CurrencyHKD) != 0 || fundOf(got, store.CurrencyCNY) != 10000 {
		t.Fatalf("after drop = %+v", got.Funds)
	}

	if _, err := st.UpdateCard(card.ID, store.Card{
		Kind: store.CardDebit, Bank: "汇丰", Last4: "1234",
		Funds: []store.CardFund{
			{Currency: store.CurrencyCNY, Balance: 10000},
			{Currency: store.CurrencyUSD, Balance: 2000},
		},
	}, nil); err != nil {
		t.Fatalf("add USD: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-04",
		UserID: admin, CardID: card.ID, Currency: store.CurrencyUSD,
	}); err != nil {
		t.Fatalf("usd expense: %v", err)
	}
	if _, err := st.UpdateCard(card.ID, store.Card{
		Kind: store.CardDebit, Bank: "汇丰", Last4: "1234",
		Funds: []store.CardFund{{Currency: store.CurrencyCNY, Balance: 10000}},
	}, nil); err == nil {
		t.Fatal("dropping used USD was accepted")
	}
}

func TestMigrateV4AddsFunds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (4)`,
		`CREATE TABLE users (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  username TEXT NOT NULL UNIQUE,
		  password_hash TEXT NOT NULL,
		  is_admin INTEGER NOT NULL DEFAULT 0,
		  token_version INTEGER NOT NULL DEFAULT 1,
		  created_at TEXT NOT NULL)`,
		`CREATE TABLE categories (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  name TEXT NOT NULL,
		  kind TEXT NOT NULL,
		  icon TEXT NOT NULL DEFAULT '',
		  color TEXT NOT NULL DEFAULT '',
		  sort_order INTEGER NOT NULL DEFAULT 0,
		  archived INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE cards (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind TEXT NOT NULL,
		  bank TEXT NOT NULL,
		  name TEXT NOT NULL DEFAULT '',
		  last4 TEXT NOT NULL,
		  balance_offset INTEGER NOT NULL DEFAULT 0,
		  archived INTEGER NOT NULL DEFAULT 0,
		  sort_order INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE transactions (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind TEXT NOT NULL CHECK (kind IN ('expense', 'income', 'transfer')),
		  amount INTEGER NOT NULL,
		  category_id INTEGER REFERENCES categories(id),
		  card_id INTEGER REFERENCES cards(id),
		  to_card_id INTEGER REFERENCES cards(id),
		  user_id INTEGER NOT NULL REFERENCES users(id),
		  date TEXT NOT NULL,
		  note TEXT NOT NULL DEFAULT '',
		  shared INTEGER NOT NULL DEFAULT 0,
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at)
		 VALUES ('mama', 'x', 1, 1, '2026-03-04T00:00:00Z')`,
		`INSERT INTO categories (name, kind, icon, color, sort_order) VALUES ('餐饮', 'expense', '', '', 1)`,
		`INSERT INTO cards (kind, bank, name, last4, balance_offset, archived, sort_order)
		 VALUES ('debit', '招商', '', '1234', 8000, 0, 0)`,
		`INSERT INTO transactions (kind, amount, category_id, card_id, user_id, date, note, shared, created_at, updated_at)
		 VALUES ('expense', 1000, 1, 1, 1, '2026-03-04', '午饭', 0, '2026-03-04T00:00:00Z', '2026-03-04T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v4: %v\n%s", err, statement)
		}
	}
	db.Close()

	st, err := store.Open(path, testAdmin)
	if err != nil {
		t.Fatalf("open v4 database: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	listed, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list after migrate: %v", err)
	}
	if len(listed) != 1 || listed[0].Note != "午饭" || listed[0].Currency != store.CurrencyCNY {
		t.Fatalf("migrated row = %+v", listed)
	}

	cards, err := st.Cards()
	if err != nil || len(cards) != 1 {
		t.Fatalf("cards after migrate = %+v (%v)", cards, err)
	}
	if fundOf(cards[0], store.CurrencyCNY) != 7000 {
		t.Errorf("migrated card funds = %+v, want CNY 7000", cards[0].Funds)
	}
}

func fundOf(card store.Card, currency string) int64 {
	for _, fund := range card.Funds {
		if fund.Currency == currency {
			return fund.Balance
		}
	}
	return 0
}

func currencyFlowOf(flows []store.CurrencyFlow, currency string) store.CurrencyFlow {
	for _, item := range flows {
		if item.Currency == currency {
			return item
		}
	}
	return store.CurrencyFlow{}
}

func totalOf(totals []store.CurrencyTotal, currency string) int64 {
	for _, item := range totals {
		if item.Currency == currency {
			return item.Amount
		}
	}
	return 0
}
