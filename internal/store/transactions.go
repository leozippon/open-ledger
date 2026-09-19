package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ledger/internal/money"
)

// Transaction kinds.
const (
	KindExpense  = "expense"
	KindIncome   = "income"
	KindTransfer = "transfer"
	KindExchange = "exchange"
)

// Transaction is one ledger entry. The display fields are joined in on read so
// the web app can render a list without extra lookups.
type Transaction struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Amount     int64  `json:"amount"`
	CategoryID int64  `json:"category_id"`
	ActivityID int64  `json:"activity_id"`
	CardID     int64  `json:"card_id"`
	ToCardID   int64  `json:"to_card_id"`
	UserID     int64  `json:"user_id"`
	Date       string `json:"date"`
	Note       string `json:"note"`
	Shared     bool   `json:"shared"`
	Currency   string `json:"currency"`
	ToAmount   int64  `json:"to_amount"`
	ToCurrency string `json:"to_currency"`

	CategoryName  string `json:"category_name"`
	CategoryIcon  string `json:"category_icon"`
	CategoryColor string `json:"category_color"`
	ActivityName  string `json:"activity_name"`
	Username      string `json:"username"`
	CardBank      string `json:"card_bank"`
	CardName      string `json:"card_name"`
	CardLast4     string `json:"card_last4"`
	CardKind      string `json:"card_kind"`
	ToCardBank    string `json:"to_card_bank"`
	ToCardName    string `json:"to_card_name"`
	ToCardLast4   string `json:"to_card_last4"`
	ToCardKind    string `json:"to_card_kind"`
}

// TxInput is the writable shape of a transaction.
type TxInput struct {
	Kind       string
	Amount     int64
	CategoryID int64
	ActivityID int64
	CardID     int64
	ToCardID   int64
	UserID     int64
	Date       string
	Note       string
	Shared     bool
	Currency   string
	ToAmount   int64
	ToCurrency string
}

// TxFilter selects transactions for listing. Month is required. SharedOnly
// selects the shared book; UserID selects one member's personal book.
type TxFilter struct {
	Month      string
	Kind       string
	CategoryID int64
	ActivityID int64
	UserID     int64
	SharedOnly bool
	Query      string
}

const txColumns = `
	t.id, t.kind, t.amount, t.category_id, t.activity_id, t.card_id, t.to_card_id, t.user_id, t.date, t.note, t.shared,
	t.currency, t.to_amount, t.to_currency,
	c.name, c.icon, c.color, a.name, u.username,
	fc.bank, fc.name, fc.last4, fc.kind, tc.bank, tc.name, tc.last4, tc.kind`

const txFrom = `
	FROM transactions t
	LEFT JOIN categories c ON c.id = t.category_id
	LEFT JOIN activities a ON a.id = t.activity_id
	JOIN users u ON u.id = t.user_id
	LEFT JOIN cards fc ON fc.id = t.card_id
	LEFT JOIN cards tc ON tc.id = t.to_card_id`

// Transactions lists one month of entries, newest first.
func (s *Store) Transactions(f TxFilter) ([]Transaction, error) {
	if err := checkMonth(f.Month); err != nil {
		return nil, err
	}
	from, to := monthRange(f.Month)
	where := []string{"t.date BETWEEN ? AND ?"}
	args := []any{from, to}
	if f.Kind != "" {
		if err := checkKind(f.Kind); err != nil {
			return nil, err
		}
		where = append(where, "t.kind = ?")
		args = append(args, f.Kind)
	}
	if f.CategoryID > 0 {
		where = append(where, "t.category_id = ?")
		args = append(args, f.CategoryID)
	}
	if f.ActivityID > 0 {
		where = append(where, "t.activity_id = ?")
		args = append(args, f.ActivityID)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, `t.note LIKE ? ESCAPE '\'`)
		args = append(args, likePattern(q))
	}
	scope, scopeArgs := txScope("t.", f.UserID, f.SharedOnly)
	args = append(args, scopeArgs...)
	query := `SELECT` + txColumns + txFrom +
		` WHERE ` + strings.Join(where, " AND ") + scope +
		` ORDER BY t.date DESC, t.id DESC`
	return s.queryTxs("list transactions", query, args...)
}

// CreateTransaction validates and stores a new entry. The recording member is
// fixed at creation time and is never rewritten by an edit.
func (s *Store) CreateTransaction(in TxInput) (Transaction, error) {
	if err := s.validateTx(&in); err != nil {
		return Transaction{}, err
	}
	ok, err := s.exists(`SELECT 1 FROM users WHERE id = ?`, in.UserID)
	if err != nil {
		return Transaction{}, err
	}
	if !ok {
		return Transaction{}, invalid("记录人不存在")
	}
	ts := now()
	res, err := s.db.Exec(`
		INSERT INTO transactions
		  (kind, amount, category_id, activity_id, card_id, to_card_id, user_id, date, note, shared, currency, to_amount, to_currency, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Kind, in.Amount, nullZero(in.CategoryID), nullZero(in.ActivityID), nullZero(in.CardID), nullZero(in.ToCardID),
		in.UserID, in.Date, in.Note, boolInt(in.Shared), in.Currency, nullZero(in.ToAmount), nullZero(in.ToCurrency), ts, ts)
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Transaction{}, err
	}
	return s.Transaction(id)
}

// UpdateTransaction replaces an existing entry.
func (s *Store) UpdateTransaction(id int64, in TxInput) (Transaction, error) {
	if err := s.validateTx(&in); err != nil {
		return Transaction{}, err
	}
	res, err := s.db.Exec(`
		UPDATE transactions
		SET kind = ?, amount = ?, category_id = ?, activity_id = ?, card_id = ?, to_card_id = ?, date = ?, note = ?, shared = ?,
		    currency = ?, to_amount = ?, to_currency = ?, updated_at = ?
		WHERE id = ?`,
		in.Kind, in.Amount, nullZero(in.CategoryID), nullZero(in.ActivityID), nullZero(in.CardID), nullZero(in.ToCardID),
		in.Date, in.Note, boolInt(in.Shared), in.Currency, nullZero(in.ToAmount), nullZero(in.ToCurrency), now(), id)
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Transaction{}, ErrNotFound
	}
	return s.Transaction(id)
}

// DeleteTransaction removes an entry.
func (s *Store) DeleteTransaction(id int64) error {
	res, err := s.db.Exec(`DELETE FROM transactions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Transaction reads a single entry with its display fields.
func (s *Store) Transaction(id int64) (Transaction, error) {
	rows, err := s.db.Query(`SELECT`+txColumns+txFrom+` WHERE t.id = ?`, id)
	if err != nil {
		return Transaction{}, fmt.Errorf("read transaction: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Transaction{}, err
		}
		return Transaction{}, ErrNotFound
	}
	return scanTx(rows)
}

// RecentTransactions returns the newest entries across all months, for
// recognition context. The limit is how many rows to keep.
func (s *Store) RecentTransactions(limit int) ([]Transaction, error) {
	if limit <= 0 {
		limit = 40
	}
	return s.queryTxs("recent transactions", `SELECT`+txColumns+txFrom+` ORDER BY t.date DESC, t.id DESC LIMIT ?`, limit)
}

// AllTransactions returns every entry oldest first, for export.
func (s *Store) AllTransactions() ([]Transaction, error) {
	return s.queryTxs("export transactions", `SELECT`+txColumns+txFrom+` ORDER BY t.date, t.id`)
}

func (s *Store) queryTxs(op, query string, args ...any) ([]Transaction, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer rows.Close()
	out := []Transaction{}
	for rows.Next() {
		t, err := scanTx(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTx(rows *sql.Rows) (Transaction, error) {
	var t Transaction
	var shared int
	var categoryID, activityID, cardID, toCardID, toAmount sql.NullInt64
	var catName, catIcon, catColor, activityName, toCurrency sql.NullString
	var cardBank, cardName, cardLast4, cardKind sql.NullString
	var toBank, toName, toLast4, toKind sql.NullString
	err := rows.Scan(
		&t.ID, &t.Kind, &t.Amount, &categoryID, &activityID, &cardID, &toCardID, &t.UserID, &t.Date, &t.Note, &shared,
		&t.Currency, &toAmount, &toCurrency,
		&catName, &catIcon, &catColor, &activityName, &t.Username,
		&cardBank, &cardName, &cardLast4, &cardKind, &toBank, &toName, &toLast4, &toKind)
	t.CategoryID = categoryID.Int64
	t.ActivityID = activityID.Int64
	t.ActivityName = activityName.String
	t.CardID = cardID.Int64
	t.ToCardID = toCardID.Int64
	t.ToAmount = toAmount.Int64
	t.ToCurrency = toCurrency.String
	t.CategoryName = catName.String
	t.CategoryIcon = catIcon.String
	t.CategoryColor = catColor.String
	t.CardBank = cardBank.String
	t.CardName = cardName.String
	t.CardLast4 = cardLast4.String
	t.CardKind = cardKind.String
	t.ToCardBank = toBank.String
	t.ToCardName = toName.String
	t.ToCardLast4 = toLast4.String
	t.ToCardKind = toKind.String
	t.Shared = shared != 0
	return t, err
}

func nullZero[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func boolInt(ok bool) int {
	if ok {
		return 1
	}
	return 0
}

func checkKind(kind string) error {
	switch kind {
	case KindExpense, KindIncome, KindTransfer, KindExchange:
		return nil
	}
	return invalid("类型只能是支出、收入、转账或兑换")
}

// validateTx enforces every rule the UI must not be able to bypass.
func (s *Store) validateTx(in *TxInput) error {
	if err := checkKind(in.Kind); err != nil {
		return err
	}
	if err := money.Validate(in.Amount); err != nil {
		return InvalidError{Msg: err.Error()}
	}
	if err := checkDate(in.Date); err != nil {
		return err
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 100 {
		return invalid("备注最多 100 个字")
	}
	in.Currency = normalizeCurrency(in.Currency)
	if err := checkCurrency(in.Currency); err != nil {
		return err
	}
	if in.Kind == KindTransfer {
		in.ActivityID = 0
		return s.validateTransfer(in)
	}
	if in.Kind == KindExchange {
		in.ActivityID = 0
		return s.validateExchange(in)
	}
	if err := s.resolveActivity(in); err != nil {
		return err
	}
	if in.ToCardID != 0 {
		return invalid("收支不能填写转入卡")
	}
	if in.ToAmount != 0 || in.ToCurrency != "" {
		return invalid("收支不能填写兑换金额")
	}
	if in.CardID != 0 {
		if err := s.cardExists(in.CardID); err != nil {
			return err
		}
	}
	if in.CategoryID <= 0 {
		return invalid("请选择分类")
	}
	var kind string
	err := s.db.QueryRow(`SELECT kind FROM categories WHERE id = ?`, in.CategoryID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid("分类不存在")
	}
	if err != nil {
		return err
	}
	if kind != in.Kind {
		return invalid("分类与类型不匹配")
	}
	return nil
}

func (s *Store) validateTransfer(in *TxInput) error {
	in.Shared = false
	in.CategoryID = 0
	in.ToAmount = 0
	in.ToCurrency = ""
	if in.CardID == 0 || in.ToCardID == 0 {
		return invalid("请选择转出和转入的卡")
	}
	if in.CardID == in.ToCardID {
		return invalid("转出和转入不能是同一张卡")
	}
	if err := s.cardExists(in.CardID); err != nil {
		return err
	}
	return s.cardExists(in.ToCardID)
}

func (s *Store) validateExchange(in *TxInput) error {
	in.Shared = false
	in.CategoryID = 0
	in.ToCardID = 0
	if in.CardID == 0 {
		return invalid("请选择银行卡")
	}
	if err := s.cardExists(in.CardID); err != nil {
		return err
	}
	if err := money.Validate(in.ToAmount); err != nil {
		return InvalidError{Msg: err.Error()}
	}
	in.ToCurrency = normalizeCurrency(in.ToCurrency)
	if err := checkCurrency(in.ToCurrency); err != nil {
		return err
	}
	if in.Currency == in.ToCurrency {
		return invalid("买入和卖出不能是同一种货币")
	}
	return nil
}
