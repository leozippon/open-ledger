package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"ledger/internal/money"
)

const (
	defaultActivityName = "日常生活"
	activityRecent      = 3
)

// Activity is an event envelope around income and expense, orthogonal to category.
type Activity struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	StartDate    string `json:"start_date"`
	EndDate      string `json:"end_date"`
	Budget       int64  `json:"budget"`
	TotalBudget  int64  `json:"total_budget"`
	IsDefault    bool   `json:"is_default"`
	SortOrder    int    `json:"sort_order"`
}

// ActivityMonth is one activity's totals in the selected book and month.
// Used and TotalUsed are family-wide: the cap sits on the activity, not a book.
type ActivityMonth struct {
	Activity
	Income    int64         `json:"income"`
	Expense   int64         `json:"expense"`
	Used      int64         `json:"used"`
	TotalUsed int64         `json:"total_used"`
	Recent    []Transaction `json:"recent"`
}

// NoteSearch is the matching income and expense for a note keyword, across months.
type NoteSearch struct {
	Transactions []Transaction `json:"transactions"`
	Expense      int64         `json:"expense"`
	Income       int64         `json:"income"`
}

// Activities lists every activity in the saved display order.
func (s *Store) Activities() ([]Activity, error) {
	rows, err := s.db.Query(`
		SELECT id, name, start_date, end_date, budget, total_budget, is_default, sort_order
		FROM activities
		ORDER BY sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("list activities: %w", err)
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		item, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Activity reads one activity.
func (s *Store) Activity(id int64) (Activity, error) {
	rows, err := s.db.Query(`
		SELECT id, name, start_date, end_date, budget, total_budget, is_default, sort_order
		FROM activities WHERE id = ?`, id)
	if err != nil {
		return Activity{}, fmt.Errorf("read activity: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Activity{}, err
		}
		return Activity{}, ErrNotFound
	}
	return scanActivity(rows)
}

// DefaultActivity is the fallback envelope for income and expense.
func (s *Store) DefaultActivity() (Activity, error) {
	rows, err := s.db.Query(`
		SELECT id, name, start_date, end_date, budget, total_budget, is_default, sort_order
		FROM activities WHERE is_default = 1 LIMIT 1`)
	if err != nil {
		return Activity{}, fmt.Errorf("default activity: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Activity{}, err
		}
		return Activity{}, ErrNotFound
	}
	return scanActivity(rows)
}

// CreateActivity inserts an activity.
func (s *Store) CreateActivity(in Activity) (Activity, error) {
	if err := normalizeActivity(&in); err != nil {
		return Activity{}, err
	}
	res, err := s.db.Exec(`
		INSERT INTO activities (name, start_date, end_date, budget, total_budget, is_default, sort_order)
		VALUES (?, ?, ?, ?, ?, 0, COALESCE((SELECT MAX(sort_order) + 1 FROM activities), 0))`,
		in.Name, in.StartDate, in.EndDate, in.Budget, in.TotalBudget)
	if err != nil {
		return Activity{}, uniqueName(err, "活动")
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Activity{}, err
	}
	return s.Activity(id)
}

// UpdateActivity replaces the editable fields; the default flag is untouched.
func (s *Store) UpdateActivity(id int64, in Activity) (Activity, error) {
	if err := normalizeActivity(&in); err != nil {
		return Activity{}, err
	}
	res, err := s.db.Exec(
		`UPDATE activities SET name = ?, start_date = ?, end_date = ?, budget = ?, total_budget = ? WHERE id = ?`,
		in.Name, in.StartDate, in.EndDate, in.Budget, in.TotalBudget, id)
	if err != nil {
		return Activity{}, uniqueName(err, "活动")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Activity{}, ErrNotFound
	}
	return s.Activity(id)
}

// DeleteActivity removes an unused non-default activity.
func (s *Store) DeleteActivity(id int64) error {
	item, err := s.Activity(id)
	if err != nil {
		return err
	}
	if item.IsDefault {
		return invalid("默认活动不能删除")
	}
	used, err := s.exists(`SELECT 1 FROM transactions WHERE activity_id = ? LIMIT 1`, id)
	if err != nil {
		return err
	}
	if used {
		return ErrInUse
	}
	res, err := s.db.Exec(`DELETE FROM activities WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete activity: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderActivities writes the display order. The ids must be exactly the
// current set. Ledger boxes stay newest-bill-first and ignore this order.
func (s *Store) ReorderActivities(ids []int64) error {
	current, err := s.Activities()
	if err != nil {
		return err
	}
	want := map[int64]struct{}{}
	for _, item := range current {
		want[item.ID] = struct{}{}
	}
	if err := exactIDs(ids, want, "活动顺序与现有活动不一致"); err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(`UPDATE activities SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return fmt.Errorf("reorder activities: %w", err)
			}
		}
		return nil
	})
}

// ActivityMonths lists activities that have income or expense in the month,
// newest bill first. Income, expense and recent bills follow the selected book;
// Used is the family's month expense and TotalUsed is the family's all-time expense.
func (s *Store) ActivityMonths(month string, userID int64, sharedOnly bool) ([]ActivityMonth, error) {
	if err := checkMonth(month); err != nil {
		return nil, err
	}
	from, to := monthRange(month)
	scope, scopeArgs := txScope("t.", userID, sharedOnly)
	rows, err := s.db.Query(`
		SELECT a.id, a.name, a.start_date, a.end_date, a.budget, a.total_budget, a.is_default, a.sort_order,
		  COALESCE(SUM(CASE WHEN t.kind = 'income' THEN t.amount END), 0),
		  COALESCE(SUM(CASE WHEN t.kind = 'expense' THEN t.amount END), 0),
		  MAX(t.date || printf('%010d', t.id))
		FROM activities a
		JOIN transactions t ON t.activity_id = a.id
		WHERE t.date BETWEEN ? AND ? AND t.kind IN ('income', 'expense')`+scope+`
		GROUP BY a.id
		ORDER BY MAX(t.date || printf('%010d', t.id)) DESC`,
		append([]any{from, to}, scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("activity months: %w", err)
	}
	defer rows.Close()
	out := []ActivityMonth{}
	for rows.Next() {
		var item ActivityMonth
		var def int
		var stamp string
		if err := rows.Scan(
			&item.ID, &item.Name, &item.StartDate, &item.EndDate, &item.Budget, &item.TotalBudget, &def, &item.SortOrder,
			&item.Income, &item.Expense, &stamp,
		); err != nil {
			return nil, err
		}
		item.IsDefault = def != 0
		item.Recent = []Transaction{}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(out))
	for i, item := range out {
		ids[i] = item.ID
		recent, err := s.recentActivityTxs(month, item.ID, userID, sharedOnly)
		if err != nil {
			return nil, err
		}
		out[i].Recent = recent
	}
	used, err := s.activityExpenseByID(ids, from, to)
	if err != nil {
		return nil, err
	}
	totals, err := s.activityExpenseByID(ids, "", "")
	if err != nil {
		return nil, err
	}
	for i, item := range out {
		out[i].Used = used[item.ID]
		out[i].TotalUsed = totals[item.ID]
	}
	return out, nil
}

func (s *Store) recentActivityTxs(month string, activityID, userID int64, sharedOnly bool) ([]Transaction, error) {
	from, to := monthRange(month)
	scope, scopeArgs := txScope("t.", userID, sharedOnly)
	args := append([]any{from, to, activityID}, scopeArgs...)
	return s.queryTxs("activity recent",
		`SELECT`+txColumns+txFrom+
			` WHERE t.date BETWEEN ? AND ? AND t.activity_id = ? AND t.kind IN ('income', 'expense')`+scope+
			` ORDER BY t.date DESC, t.id DESC LIMIT ?`,
		append(args, activityRecent)...)
}

// SearchNotes finds income and expense whose note contains q, across all months.
func (s *Store) SearchNotes(q string, userID int64, sharedOnly bool) (NoteSearch, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return NoteSearch{Transactions: []Transaction{}}, nil
	}
	scope, scopeArgs := txScope("t.", userID, sharedOnly)
	args := append([]any{likePattern(q)}, scopeArgs...)
	txs, err := s.queryTxs("search notes",
		`SELECT`+txColumns+txFrom+
			` WHERE t.kind IN ('income', 'expense') AND t.note LIKE ? ESCAPE '\'`+scope+
			` ORDER BY t.date DESC, t.id DESC`,
		args...)
	if err != nil {
		return NoteSearch{}, err
	}
	out := NoteSearch{Transactions: txs}
	for _, tx := range txs {
		if tx.Kind == KindExpense {
			out.Expense += tx.Amount
		}
		if tx.Kind == KindIncome {
			out.Income += tx.Amount
		}
	}
	return out, nil
}

func (s *Store) activityExpenseByID(ids []int64, from, to string) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	holders := strings.Repeat("?,", len(ids))
	holders = holders[:len(holders)-1]
	when, whenArgs := dateClause("date", from, to)
	args := make([]any, 0, len(ids)+len(whenArgs))
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, whenArgs...)
	rows, err := s.db.Query(
		`SELECT activity_id, COALESCE(SUM(amount), 0) FROM transactions
		 WHERE kind = 'expense' AND activity_id IN (`+holders+`)`+when+`
		 GROUP BY activity_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("activity expense: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, total int64
		if err := rows.Scan(&id, &total); err != nil {
			return nil, err
		}
		out[id] = total
	}
	return out, rows.Err()
}

func (s *Store) resolveActivity(in *TxInput) error {
	if in.Kind == KindTransfer || in.Kind == KindExchange {
		in.ActivityID = 0
		return nil
	}
	if in.ActivityID == 0 {
		def, err := s.DefaultActivity()
		if err != nil {
			return err
		}
		in.ActivityID = def.ID
		return nil
	}
	ok, err := s.exists(`SELECT 1 FROM activities WHERE id = ?`, in.ActivityID)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("活动不存在")
	}
	return nil
}

func normalizeActivity(in *Activity) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return invalid("请填写活动名称")
	}
	if utf8.RuneCountInString(in.Name) > 20 {
		return invalid("活动名称最多 20 个字")
	}
	in.EndDate = ""
	if strings.TrimSpace(in.StartDate) == "" {
		in.StartDate = time.Now().Format("2006-01-02")
	} else if err := checkDate(in.StartDate); err != nil {
		return err
	}
	if err := checkCap(in.Budget); err != nil {
		return err
	}
	return checkCap(in.TotalBudget)
}

func checkCap(cents int64) error {
	if cents < 0 {
		return invalid("预算不能为负数")
	}
	if cents > money.MaxCents {
		return invalid("金额过大")
	}
	return nil
}

func scanActivity(rows *sql.Rows) (Activity, error) {
	var item Activity
	var def int
	err := rows.Scan(&item.ID, &item.Name, &item.StartDate, &item.EndDate, &item.Budget, &item.TotalBudget, &def, &item.SortOrder)
	item.IsDefault = def != 0
	return item, err
}

func activityClause(prefix string, activityID int64) (string, []any) {
	if activityID <= 0 {
		return "", nil
	}
	return " AND " + prefix + "activity_id = ?", []any{activityID}
}
