package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"ledger/internal/store"
)

func TestActivitiesSeedAndAssign(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")

	items, err := st.Activities()
	if err != nil {
		t.Fatalf("list activities: %v", err)
	}
	if len(items) != 1 || items[0].Name != "日常生活" || !items[0].IsDefault || items[0].EndDate != "" {
		t.Fatalf("seeded activities = %+v, want one open-ended 日常生活", items)
	}
	daily := items[0]

	created, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 1250, CategoryID: food, Date: "2026-03-04", UserID: admin, Note: "午饭",
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if created.ActivityID != daily.ID || created.ActivityName != "日常生活" {
		t.Fatalf("default activity = %d %q, want %d 日常生活", created.ActivityID, created.ActivityName, daily.ID)
	}

	trip, err := st.CreateActivity(store.Activity{
		Name: "香港之旅", StartDate: "2026-03-01", EndDate: "2026-03-08", Budget: 500000,
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	if trip.EndDate != "" {
		t.Fatalf("activity end date = %q, want empty", trip.EndDate)
	}

	later, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 8800, CategoryID: food, Date: "2026-03-05",
		UserID: admin, ActivityID: trip.ID, Note: "高铁",
	})
	if err != nil {
		t.Fatalf("create trip expense: %v", err)
	}
	if later.ActivityID != trip.ID {
		t.Fatalf("trip expense activity = %d, want %d", later.ActivityID, trip.ID)
	}

	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-05",
		UserID: admin, ActivityID: 9999,
	}); err == nil {
		t.Fatal("missing activity was accepted")
	}

	months, err := st.ActivityMonths("2026-03", 0, false)
	if err != nil {
		t.Fatalf("activity months: %v", err)
	}
	if len(months) != 2 {
		t.Fatalf("activity months = %+v, want 日常生活 and 香港之旅", months)
	}
	if months[0].ID != trip.ID || months[1].ID != daily.ID {
		t.Fatalf("activity order = %q then %q, want newest bill first", months[0].Name, months[1].Name)
	}
	if months[0].Used != 8800 || months[0].Expense != 8800 {
		t.Errorf("trip used/expense = %d/%d, want 8800/8800", months[0].Used, months[0].Expense)
	}
	if len(months[0].Recent) != 1 || months[0].Recent[0].Note != "高铁" {
		t.Errorf("trip recent = %+v, want 高铁", months[0].Recent)
	}
	if len(months[1].Recent) != 1 || months[1].Recent[0].Note != "午饭" {
		t.Errorf("daily recent = %+v, want 午饭", months[1].Recent)
	}

	filtered, err := st.SummaryFiltered("2026-03", 0, false, trip.ID)
	if err != nil {
		t.Fatalf("filtered summary: %v", err)
	}
	if filtered.Expense != 8800 || filtered.Budget != 500000 {
		t.Errorf("trip summary = expense %d budget %d, want 8800/500000", filtered.Expense, filtered.Budget)
	}
	all, err := st.Summary("2026-03", 0, false)
	if err != nil {
		t.Fatalf("family summary: %v", err)
	}
	if all.Expense != 10050 {
		t.Errorf("family expense = %d, want 10050", all.Expense)
	}

	for _, note := range []string{"早", "午", "晚", "夜"} {
		if _, err := st.CreateTransaction(store.TxInput{
			Kind: store.KindExpense, Amount: 100, CategoryID: food, Date: "2026-03-06",
			UserID: admin, Note: note,
		}); err != nil {
			t.Fatalf("extra daily %s: %v", note, err)
		}
	}
	capped, err := st.ActivityMonths("2026-03", 0, false)
	if err != nil {
		t.Fatalf("activity months after extras: %v", err)
	}
	var got []string
	for _, item := range capped {
		if item.ID != daily.ID {
			continue
		}
		for _, tx := range item.Recent {
			got = append(got, tx.Note)
		}
	}
	if len(got) != 3 || got[0] != "夜" || got[2] != "午" {
		t.Errorf("daily recent cap = %v, want [夜 晚 午]", got)
	}
}

func TestTransferHasNoActivity(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	from, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "1111", Balance: 10000})
	if err != nil {
		t.Fatalf("from card: %v", err)
	}
	to, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "2222", Balance: 0})
	if err != nil {
		t.Fatalf("to card: %v", err)
	}
	daily, err := st.DefaultActivity()
	if err != nil {
		t.Fatalf("default activity: %v", err)
	}
	tx, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindTransfer, Amount: 1000, CardID: from.ID, ToCardID: to.ID,
		Date: "2026-03-04", UserID: admin, ActivityID: daily.ID,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tx.ActivityID != 0 || tx.ActivityName != "" {
		t.Fatalf("transfer kept activity %d %q", tx.ActivityID, tx.ActivityName)
	}
}

func TestDeleteActivityRules(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	daily, err := st.DefaultActivity()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if err := st.DeleteActivity(daily.ID); err == nil {
		t.Fatal("deleting the default activity was accepted")
	}

	trip, err := st.CreateActivity(store.Activity{Name: "短途", StartDate: "2026-03-01", EndDate: "2026-03-02"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 500, CategoryID: food, Date: "2026-03-01",
		UserID: admin, ActivityID: trip.ID,
	}); err != nil {
		t.Fatalf("expense: %v", err)
	}
	if err := st.DeleteActivity(trip.ID); !errors.Is(err, store.ErrInUse) {
		t.Fatalf("delete used activity = %v, want ErrInUse", err)
	}

	spare, err := st.CreateActivity(store.Activity{Name: "空活动", StartDate: "2026-03-01"})
	if err != nil {
		t.Fatalf("spare: %v", err)
	}
	if err := st.DeleteActivity(spare.ID); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
}

func TestSearchNotesTotals(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	salary := categoryID(t, st, store.KindIncome, "工资")
	from, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "1111", Balance: 20000})
	if err != nil {
		t.Fatalf("from card: %v", err)
	}
	to, err := st.CreateCard(store.Card{Kind: store.CardDebit, Bank: "招商", Last4: "2222", Balance: 0})
	if err != nil {
		t.Fatalf("to card: %v", err)
	}

	for i, entry := range []store.TxInput{
		{Kind: store.KindExpense, Amount: 2600, CategoryID: food, Date: "2026-02-04", Note: "喜茶"},
		{Kind: store.KindExpense, Amount: 1800, CategoryID: food, Date: "2026-03-04", Note: "喜茶 中杯"},
		{Kind: store.KindIncome, Amount: 5000, CategoryID: salary, Date: "2026-03-10", Note: "喜茶报销"},
		{Kind: store.KindTransfer, Amount: 1000, CardID: from.ID, ToCardID: to.ID, Date: "2026-03-04", Note: "喜茶备用金"},
	} {
		entry.UserID = admin
		if _, err := st.CreateTransaction(entry); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	found, err := st.SearchNotes("喜茶", 0, false)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Transactions) != 3 {
		t.Fatalf("search hits = %d, want 3 income/expense rows: %+v", len(found.Transactions), found.Transactions)
	}
	if found.Expense != 4400 || found.Income != 5000 {
		t.Errorf("search totals = expense %d income %d, want 4400/5000", found.Expense, found.Income)
	}
}

func TestReorderActivities(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	daily, err := st.DefaultActivity()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	trip, err := st.CreateActivity(store.Activity{Name: "香港之旅"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	listed, err := st.Activities()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != daily.ID || listed[1].ID != trip.ID {
		t.Fatalf("seeded order = %+v, want 日常生活 then 香港之旅", listed)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 8800, CategoryID: food, Date: "2026-03-04",
		UserID: admin, ActivityID: trip.ID, Note: "高铁",
	}); err != nil {
		t.Fatalf("trip bill: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 1250, CategoryID: food, Date: "2026-03-05",
		UserID: admin, ActivityID: daily.ID, Note: "午饭",
	}); err != nil {
		t.Fatalf("daily bill: %v", err)
	}
	if err := st.ReorderActivities([]int64{trip.ID, daily.ID}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	listed, err = st.Activities()
	if err != nil {
		t.Fatalf("list after reorder: %v", err)
	}
	if listed[0].ID != trip.ID || listed[1].ID != daily.ID {
		t.Errorf("reordered = %q then %q, want 香港之旅 then 日常生活", listed[0].Name, listed[1].Name)
	}
	months, err := st.ActivityMonths("2026-03", 0, false)
	if err != nil {
		t.Fatalf("months: %v", err)
	}
	if len(months) != 2 || months[0].ID != daily.ID || months[1].ID != trip.ID {
		t.Errorf("ledger boxes = %+v, want newest bill first", months)
	}
	if err := st.ReorderActivities([]int64{trip.ID}); err == nil {
		t.Error("a partial order was accepted")
	}
}

func TestActivityTotalBudget(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	trip, err := st.CreateActivity(store.Activity{Name: "香港之旅", Budget: 100000, TotalBudget: 500000})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if trip.Budget != 100000 || trip.TotalBudget != 500000 {
		t.Fatalf("caps = %+v, want monthly 100000 and total 500000", trip)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 8800, CategoryID: food, Date: "2026-02-18",
		UserID: admin, ActivityID: trip.ID, Note: "订金",
	}); err != nil {
		t.Fatalf("feb: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 2600, CategoryID: food, Date: "2026-03-05",
		UserID: admin, ActivityID: trip.ID, Note: "高铁",
	}); err != nil {
		t.Fatalf("mar: %v", err)
	}
	months, err := st.ActivityMonths("2026-03", 0, false)
	if err != nil {
		t.Fatalf("months: %v", err)
	}
	if len(months) != 1 || months[0].Used != 2600 || months[0].TotalUsed != 11400 {
		t.Fatalf("march used/total = %+v, want 2600/11400", months)
	}
}

func TestActivityCapIsFamilyWide(t *testing.T) {
	st := newStore(t)
	admin := adminID(t, st)
	food := categoryID(t, st, store.KindExpense, "餐饮")
	other, err := st.CreateUser("papa", "another-secret", false)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	trip, err := st.CreateActivity(store.Activity{Name: "香港之旅", Budget: 800000, TotalBudget: 800000})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 100000, CategoryID: food, Date: "2026-03-04",
		UserID: admin, ActivityID: trip.ID, Note: "高铁",
	}); err != nil {
		t.Fatalf("admin spend: %v", err)
	}
	if _, err := st.CreateTransaction(store.TxInput{
		Kind: store.KindExpense, Amount: 200000, CategoryID: food, Date: "2026-03-05",
		UserID: other.ID, ActivityID: trip.ID, Note: "酒店",
	}); err != nil {
		t.Fatalf("member spend: %v", err)
	}

	mine, err := st.ActivityMonths("2026-03", admin, false)
	if err != nil {
		t.Fatalf("admin book: %v", err)
	}
	if len(mine) != 1 || mine[0].Expense != 100000 || mine[0].Used != 300000 || mine[0].TotalUsed != 300000 {
		t.Fatalf("admin book = expense %d used %d/%d, want 100000 and family 300000/300000", mine[0].Expense, mine[0].Used, mine[0].TotalUsed)
	}

	family, err := st.ActivityMonths("2026-03", 0, false)
	if err != nil {
		t.Fatalf("family book: %v", err)
	}
	if len(family) != 1 || family[0].Expense != 300000 || family[0].Used != 300000 {
		t.Fatalf("family book = expense %d used %d, want 300000/300000", family[0].Expense, family[0].Used)
	}
}

func TestBudgetFollowsDefaultActivity(t *testing.T) {
	st := newStore(t)
	if err := st.SetBudget(300000); err != nil {
		t.Fatalf("set budget: %v", err)
	}
	daily, err := st.DefaultActivity()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if daily.Budget != 300000 {
		t.Errorf("default activity budget = %d, want 300000", daily.Budget)
	}
	got, err := st.Budget()
	if err != nil || got != 300000 {
		t.Errorf("Budget() = %d (%v), want 300000", got, err)
	}
}

func TestMigrateV6AddsActivities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version (version) VALUES (6)`,
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
		  network TEXT NOT NULL DEFAULT '',
		  balance_offset INTEGER NOT NULL DEFAULT 0,
		  archived INTEGER NOT NULL DEFAULT 0,
		  sort_order INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE card_funds (
		  card_id INTEGER NOT NULL,
		  currency TEXT NOT NULL,
		  balance_offset INTEGER NOT NULL DEFAULT 0,
		  PRIMARY KEY (card_id, currency))`,
		`CREATE TABLE transactions (
		  id INTEGER PRIMARY KEY AUTOINCREMENT,
		  kind TEXT NOT NULL CHECK (kind IN ('expense', 'income', 'transfer', 'exchange')),
		  amount INTEGER NOT NULL,
		  category_id INTEGER REFERENCES categories(id),
		  card_id INTEGER REFERENCES cards(id),
		  to_card_id INTEGER REFERENCES cards(id),
		  user_id INTEGER NOT NULL REFERENCES users(id),
		  date TEXT NOT NULL,
		  note TEXT NOT NULL DEFAULT '',
		  shared INTEGER NOT NULL DEFAULT 0,
		  currency TEXT NOT NULL DEFAULT 'CNY',
		  to_amount INTEGER,
		  to_currency TEXT,
		  created_at TEXT NOT NULL,
		  updated_at TEXT NOT NULL)`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO users (username, password_hash, is_admin, token_version, created_at)
		 VALUES ('mama', 'x', 1, 1, '2026-03-04T00:00:00Z')`,
		`INSERT INTO categories (name, kind, icon, color, sort_order) VALUES ('餐饮', 'expense', '', '', 1)`,
		`INSERT INTO settings (key, value) VALUES ('monthly_budget', '300000')`,
		`INSERT INTO transactions (kind, amount, category_id, user_id, date, note, shared, currency, created_at, updated_at)
		 VALUES ('expense', 1250, 1, 1, '2026-02-18', '午饭', 0, 'CNY', '2026-02-18T00:00:00Z', '2026-02-18T00:00:00Z')`,
		`INSERT INTO transactions (kind, amount, user_id, date, note, shared, currency, created_at, updated_at)
		 VALUES ('transfer', 100, 1, '2026-03-01', '转自己', 0, 'CNY', '2026-03-01T00:00:00Z', '2026-03-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed v6: %v\n%s", err, statement)
		}
	}
	db.Close()

	st, err := store.Open(path, testAdmin)
	if err != nil {
		t.Fatalf("open v6 database: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	daily, err := st.DefaultActivity()
	if err != nil {
		t.Fatalf("default after migrate: %v", err)
	}
	if daily.Name != "日常生活" || daily.Budget != 300000 || daily.StartDate != "2026-02-18" || daily.EndDate != "" {
		t.Fatalf("migrated default = %+v, want 日常生活 from 2026-02-18 with budget 300000", daily)
	}
	if got, _ := st.Budget(); got != 300000 {
		t.Errorf("Budget() after migrate = %d, want 300000", got)
	}

	listed, err := st.Transactions(store.TxFilter{Month: "2026-02"})
	if err != nil {
		t.Fatalf("list Feb: %v", err)
	}
	if len(listed) != 1 || listed[0].ActivityID != daily.ID || listed[0].ActivityName != "日常生活" {
		t.Fatalf("migrated expense = %+v, want 日常生活", listed)
	}

	moved, err := st.Transactions(store.TxFilter{Month: "2026-03"})
	if err != nil {
		t.Fatalf("list Mar: %v", err)
	}
	if len(moved) != 1 || moved[0].Kind != store.KindTransfer || moved[0].ActivityID != 0 {
		t.Fatalf("migrated transfer = %+v, want no activity", moved)
	}
}
