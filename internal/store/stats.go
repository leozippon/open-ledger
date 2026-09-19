package store

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// CategoryTotal is one slice of a month's expense or income.
type CategoryTotal struct {
	CategoryID int64  `json:"category_id"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Color      string `json:"color"`
	Amount     int64  `json:"amount"`
}

// DayTotal is one day's income and expense within a month.
type DayTotal struct {
	Date    string `json:"date"`
	Income  int64  `json:"income"`
	Expense int64  `json:"expense"`
}

// Summary is everything the month views need besides the entry list.
type Summary struct {
	Month             string          `json:"month"`
	UserID            int64           `json:"user_id"`
	Income            int64           `json:"income"`
	Expense           int64           `json:"expense"`
	Budget            int64           `json:"budget"`
	Balance           int64           `json:"balance"`
	Balances          []CurrencyTotal `json:"balances"`
	Currencies        []CurrencyFlow  `json:"currencies"`
	ExpenseByCategory []CategoryTotal `json:"expense_by_category"`
	IncomeByCategory  []CategoryTotal `json:"income_by_category"`
	ExpenseByCard     []CardTotal     `json:"expense_by_card"`
	IncomeByCard      []CardTotal     `json:"income_by_card"`
	Days              []DayTotal      `json:"days"`
}

// CardTotal is one card's income or expense in the selected window.
type CardTotal struct {
	CardID int64  `json:"card_id"`
	Bank   string `json:"bank"`
	Name   string `json:"name"`
	Last4  string `json:"last4"`
	Kind   string `json:"kind"`
	Amount int64  `json:"amount"`
}

// TrendPoint is one month of the trend chart.
type TrendPoint struct {
	Month   string `json:"month"`
	Income  int64  `json:"income"`
	Expense int64  `json:"expense"`
}

// txScope narrows an aggregate to one book. Shared entries belong to the
// shared book, not the recorder's personal one.
func txScope(prefix string, userID int64, sharedOnly bool) (string, []any) {
	if sharedOnly {
		return " AND " + prefix + "shared = 1", nil
	}
	if userID > 0 {
		return " AND " + prefix + "user_id = ? AND " + prefix + "shared = 0", []any{userID}
	}
	return "", nil
}

// Summary aggregates one month, optionally for a single book. The budget and
// the family balance (the sum of card balances) apply only to the family book.
func (s *Store) Summary(month string, userID int64, sharedOnly bool) (Summary, error) {
	return s.SummaryFiltered(month, userID, sharedOnly, 0)
}

// SummaryFiltered is Summary restricted to one activity when activityID > 0.
func (s *Store) SummaryFiltered(month string, userID int64, sharedOnly bool, activityID int64) (Summary, error) {
	if err := checkMonth(month); err != nil {
		return Summary{}, err
	}
	from, to := monthRange(month)
	return s.summaryRange(month, from, to, userID, sharedOnly, true, activityID)
}

// SummaryYear aggregates one calendar year. Daily totals are omitted; the
// stats page only needs the yearly slices and the monthly trend.
func (s *Store) SummaryYear(year string, userID int64, sharedOnly bool) (Summary, error) {
	return s.SummaryYearFiltered(year, userID, sharedOnly, 0)
}

// SummaryYearFiltered is SummaryYear restricted to one activity when activityID > 0.
func (s *Store) SummaryYearFiltered(year string, userID int64, sharedOnly bool, activityID int64) (Summary, error) {
	if err := checkYear(year); err != nil {
		return Summary{}, err
	}
	from, to := yearRange(year)
	return s.summaryRange(year+"-01", from, to, userID, sharedOnly, false, activityID)
}

// SummaryWindow aggregates an explicit date range.
func (s *Store) SummaryWindow(from, to string, userID int64, sharedOnly bool, activityID int64) (Summary, error) {
	if err := checkDate(from); err != nil {
		return Summary{}, err
	}
	if err := checkDate(to); err != nil {
		return Summary{}, err
	}
	if to < from {
		return Summary{}, invalid("结束日期不能早于开始日期")
	}
	return s.summaryRange(from[:7], from, to, userID, sharedOnly, true, activityID)
}

// SummaryAll aggregates every income and expense, with no date window.
func (s *Store) SummaryAll(userID int64, sharedOnly bool, activityID int64) (Summary, error) {
	return s.summaryRange("", "", "", userID, sharedOnly, false, activityID)
}

func dateClause(column, from, to string) (string, []any) {
	if from == "" || to == "" {
		return "", nil
	}
	return " AND " + column + " BETWEEN ? AND ?", []any{from, to}
}

func (s *Store) summaryRange(month, from, to string, userID int64, sharedOnly, withDays bool, activityID int64) (Summary, error) {
	scope, scopeArgs := txScope("", userID, sharedOnly)
	act, actArgs := activityClause("", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	when, whenArgs := dateClause("date", from, to)
	out := Summary{
		Month: month, UserID: userID,
		ExpenseByCategory: []CategoryTotal{}, IncomeByCategory: []CategoryTotal{},
		ExpenseByCard: []CardTotal{}, IncomeByCard: []CardTotal{}, Days: []DayTotal{},
		Balances: []CurrencyTotal{}, Currencies: []CurrencyFlow{},
	}

	if err := s.db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions WHERE 1=1`+when+scope,
		append(whenArgs, scopeArgs...)...).
		Scan(&out.Income, &out.Expense); err != nil {
		return Summary{}, fmt.Errorf("summary totals: %w", err)
	}

	budget, err := s.activityBudget(activityID)
	if err != nil {
		return Summary{}, err
	}
	out.Budget = budget

	if out.Balances, out.Balance, err = s.balances(userID, sharedOnly); err != nil {
		return Summary{}, err
	}
	if out.Currencies, err = s.currencyFlows(from, to, userID, sharedOnly, activityID); err != nil {
		return Summary{}, err
	}

	if out.ExpenseByCategory, err = s.byCategory(from, to, KindExpense, userID, sharedOnly, activityID); err != nil {
		return Summary{}, err
	}
	if out.IncomeByCategory, err = s.byCategory(from, to, KindIncome, userID, sharedOnly, activityID); err != nil {
		return Summary{}, err
	}
	if out.ExpenseByCard, err = s.byCard(from, to, KindExpense, userID, sharedOnly, activityID); err != nil {
		return Summary{}, err
	}
	if out.IncomeByCard, err = s.byCard(from, to, KindIncome, userID, sharedOnly, activityID); err != nil {
		return Summary{}, err
	}

	if !withDays {
		return out, nil
	}

	rows, err := s.db.Query(`
		SELECT date,
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions
		WHERE 1=1`+when+scope+`
		GROUP BY date ORDER BY date DESC`,
		append(whenArgs, scopeArgs...)...)
	if err != nil {
		return Summary{}, fmt.Errorf("summary days: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DayTotal
		if err := rows.Scan(&d.Date, &d.Income, &d.Expense); err != nil {
			return Summary{}, err
		}
		out.Days = append(out.Days, d)
	}
	return out, rows.Err()
}

func (s *Store) netAllTime(userID int64, sharedOnly bool) (int64, error) {
	scope, scopeArgs := txScope("", userID, sharedOnly)
	var income, expense int64
	if err := s.db.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions WHERE 1=1`+scope, scopeArgs...).
		Scan(&income, &expense); err != nil {
		return 0, fmt.Errorf("balance: %w", err)
	}
	return income - expense, nil
}

func (s *Store) balances(userID int64, sharedOnly bool) ([]CurrencyTotal, int64, error) {
	if userID != 0 || sharedOnly {
		net, err := s.netAllTime(userID, sharedOnly)
		if err != nil {
			return nil, 0, err
		}
		return []CurrencyTotal{}, net, nil
	}
	totals, err := s.FamilyBalances()
	if err != nil {
		return nil, 0, err
	}
	return totals, currencyAmount(totals, CurrencyCNY), nil
}

func (s *Store) activityBudget(activityID int64) (int64, error) {
	if activityID <= 0 {
		return s.Budget()
	}
	item, err := s.Activity(activityID)
	if err != nil {
		return 0, err
	}
	return item.Budget, nil
}

func (s *Store) currencyFlows(from, to string, userID int64, sharedOnly bool, activityID int64) ([]CurrencyFlow, error) {
	month, err := s.flowByCurrency(from, to, userID, sharedOnly, activityID)
	if err != nil {
		return nil, err
	}
	balance := map[string]int64{}
	if userID != 0 || sharedOnly {
		all, err := s.flowByCurrency("", "", userID, sharedOnly, activityID)
		if err != nil {
			return nil, err
		}
		for code, flow := range all {
			balance[code] = flow.income - flow.expense
		}
	} else {
		cards, err := s.Cards()
		if err != nil {
			return nil, err
		}
		for _, card := range cards {
			for _, fund := range card.Funds {
				balance[fund.Currency] += fund.Balance
			}
		}
	}
	out := []CurrencyFlow{}
	for _, code := range Currencies {
		flow := month[code]
		amount, hasBalance := balance[code]
		if !hasBalance && flow.income == 0 && flow.expense == 0 {
			continue
		}
		out = append(out, CurrencyFlow{Currency: code, Balance: amount, Income: flow.income, Expense: flow.expense})
	}
	if len(out) == 0 {
		return []CurrencyFlow{{Currency: CurrencyCNY}}, nil
	}
	return out, nil
}

type currencyFlow struct {
	income  int64
	expense int64
}

func (s *Store) flowByCurrency(from, to string, userID int64, sharedOnly bool, activityID int64) (map[string]currencyFlow, error) {
	scope, scopeArgs := txScope("", userID, sharedOnly)
	act, actArgs := activityClause("", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	q := `
		SELECT currency,
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions WHERE kind IN ('income', 'expense')`
	args := []any{}
	if from != "" {
		q += ` AND date BETWEEN ? AND ?`
		args = append(args, from, to)
	}
	q += scope + ` GROUP BY currency`
	args = append(args, scopeArgs...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("currency flows: %w", err)
	}
	defer rows.Close()
	out := map[string]currencyFlow{}
	for rows.Next() {
		var code string
		var flow currencyFlow
		if err := rows.Scan(&code, &flow.income, &flow.expense); err != nil {
			return nil, err
		}
		out[code] = flow
	}
	return out, rows.Err()
}

func (s *Store) byCategory(from, to, kind string, userID int64, sharedOnly bool, activityID int64) ([]CategoryTotal, error) {
	scope, scopeArgs := txScope("t.", userID, sharedOnly)
	act, actArgs := activityClause("t.", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	when, whenArgs := dateClause("t.date", from, to)
	rows, err := s.db.Query(`
		SELECT c.id, c.name, c.icon, c.color, SUM(t.amount) AS total
		FROM transactions t JOIN categories c ON c.id = t.category_id
		WHERE t.kind = ?`+when+scope+`
		GROUP BY c.id ORDER BY total DESC`,
		append(append([]any{kind}, whenArgs...), scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("summary by category: %w", err)
	}
	defer rows.Close()
	out := []CategoryTotal{}
	for rows.Next() {
		var c CategoryTotal
		if err := rows.Scan(&c.CategoryID, &c.Name, &c.Icon, &c.Color, &c.Amount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) byCard(from, to, kind string, userID int64, sharedOnly bool, activityID int64) ([]CardTotal, error) {
	scope, scopeArgs := txScope("t.", userID, sharedOnly)
	act, actArgs := activityClause("t.", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	when, whenArgs := dateClause("t.date", from, to)
	rows, err := s.db.Query(`
		SELECT COALESCE(c.id, 0), COALESCE(c.bank, ''), COALESCE(c.name, ''), COALESCE(c.last4, ''), COALESCE(c.kind, ''),
		  SUM(t.amount) AS total
		FROM transactions t
		LEFT JOIN cards c ON c.id = t.card_id
		WHERE t.kind = ?`+when+scope+`
		GROUP BY c.id
		ORDER BY CASE WHEN c.id IS NULL THEN 1 ELSE 0 END, total DESC`,
		append(append([]any{kind}, whenArgs...), scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("summary by card: %w", err)
	}
	defer rows.Close()
	out := []CardTotal{}
	for rows.Next() {
		var item CardTotal
		if err := rows.Scan(&item.CardID, &item.Bank, &item.Name, &item.Last4, &item.Kind, &item.Amount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Trend returns the last n months up to the current one, oldest first,
// optionally for a single member.
func (s *Store) Trend(n int, userID int64, sharedOnly bool) ([]TrendPoint, error) {
	return s.TrendFiltered(n, userID, sharedOnly, 0)
}

// TrendFiltered is Trend restricted to one activity when activityID > 0.
func (s *Store) TrendFiltered(n int, userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	if n < 1 || n > 60 {
		return nil, invalid("月份数量应在 1 到 60 之间")
	}
	now := time.Now()
	points := make([]TrendPoint, n)
	for i := range points {
		m := time.Date(now.Year(), now.Month()-time.Month(n-1-i), 1, 0, 0, 0, 0, now.Location())
		points[i] = TrendPoint{Month: m.Format("2006-01")}
	}
	return s.fillTrend(points, userID, sharedOnly, activityID)
}

// TrendYear returns the twelve months of a calendar year, oldest first.
func (s *Store) TrendYear(year string, userID int64, sharedOnly bool) ([]TrendPoint, error) {
	return s.TrendYearFiltered(year, userID, sharedOnly, 0)
}

// TrendYearFiltered is TrendYear restricted to one activity when activityID > 0.
func (s *Store) TrendYearFiltered(year string, userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	if err := checkYear(year); err != nil {
		return nil, err
	}
	points := make([]TrendPoint, 12)
	for i := range points {
		points[i] = TrendPoint{Month: fmt.Sprintf("%s-%02d", year, i+1)}
	}
	return s.fillTrend(points, userID, sharedOnly, activityID)
}

// TrendWindow fills one point per month overlapping [from, to].
func (s *Store) TrendWindow(from, to string, userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	if err := checkDate(from); err != nil {
		return nil, err
	}
	if err := checkDate(to); err != nil {
		return nil, err
	}
	if to < from {
		return nil, invalid("结束日期不能早于开始日期")
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil, invalid("日期格式应为 YYYY-MM-DD")
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, invalid("日期格式应为 YYYY-MM-DD")
	}
	start = time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	end = time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	points := []TrendPoint{}
	for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 1, 0) {
		points = append(points, TrendPoint{Month: cursor.Format("2006-01")})
	}
	return s.fillTrend(points, userID, sharedOnly, activityID)
}

// TrendAll returns one point per year from the first bill through this year.
func (s *Store) TrendAll(userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	var first sql.NullString
	if err := s.db.QueryRow(`SELECT MIN(substr(date, 1, 4)) FROM transactions WHERE kind IN ('income', 'expense')`).Scan(&first); err != nil {
		return nil, fmt.Errorf("trend years: %w", err)
	}
	end := time.Now().Year()
	start := end
	if first.Valid {
		if year, err := strconv.Atoi(first.String); err == nil && year > 0 && year <= end {
			start = year
		}
	}
	points := make([]TrendPoint, 0, end-start+1)
	for year := start; year <= end; year++ {
		points = append(points, TrendPoint{Month: strconv.Itoa(year)})
	}
	return s.fillYearTrend(points, userID, sharedOnly, activityID)
}

func (s *Store) fillYearTrend(points []TrendPoint, userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	if len(points) == 0 {
		return points, nil
	}
	index := make(map[string]int, len(points))
	for i, point := range points {
		index[point.Month] = i
	}
	scope, scopeArgs := txScope("", userID, sharedOnly)
	act, actArgs := activityClause("", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	rows, err := s.db.Query(`
		SELECT substr(date, 1, 4) AS y,
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions
		WHERE date >= ?`+scope+`
		GROUP BY y`,
		append([]any{points[0].Month + "-01-01"}, scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("year trend: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var year string
		var income, expense int64
		if err := rows.Scan(&year, &income, &expense); err != nil {
			return nil, err
		}
		if i, ok := index[year]; ok {
			points[i].Income, points[i].Expense = income, expense
		}
	}
	return points, rows.Err()
}

func (s *Store) fillTrend(points []TrendPoint, userID int64, sharedOnly bool, activityID int64) ([]TrendPoint, error) {
	if len(points) == 0 {
		return points, nil
	}
	index := make(map[string]int, len(points))
	for i, point := range points {
		index[point.Month] = i
	}

	scope, scopeArgs := txScope("", userID, sharedOnly)
	act, actArgs := activityClause("", activityID)
	scope += act
	scopeArgs = append(scopeArgs, actArgs...)
	rows, err := s.db.Query(`
		SELECT substr(date, 1, 7) AS m,
		  COALESCE(SUM(CASE WHEN kind = 'income'  THEN amount END), 0),
		  COALESCE(SUM(CASE WHEN kind = 'expense' THEN amount END), 0)
		FROM transactions
		WHERE date >= ?`+scope+`
		GROUP BY m`,
		append([]any{points[0].Month + "-01"}, scopeArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("trend: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var month string
		var income, expense int64
		if err := rows.Scan(&month, &income, &expense); err != nil {
			return nil, err
		}
		if i, ok := index[month]; ok {
			points[i].Income, points[i].Expense = income, expense
		}
	}
	return points, rows.Err()
}
