package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ledger/internal/money"
)

// Card kinds.
const (
	CardDebit  = "debit"
	CardCredit = "credit"
)

// Card networks shown as marks on a card. Empty means none.
const (
	NetworkUnionPay   = "unionpay"
	NetworkVisa       = "visa"
	NetworkMastercard = "mastercard"
	NetworkAmex       = "amex"
)

// networks is the display order of every card scheme the ledger accepts.
var networks = []string{
	NetworkUnionPay, NetworkVisa, NetworkMastercard, NetworkAmex,
}

// CardFund is one currency's current amount on a card.
type CardFund struct {
	Currency string `json:"currency"`
	Balance  int64  `json:"balance"`
}

// Card is a debit or credit card with per-currency running balances.
type Card struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Bank      string     `json:"bank"`
	Name      string     `json:"name"`
	Last4     string     `json:"last4"`
	Network   string     `json:"network"`
	Currency  string     `json:"currency,omitempty"`
	Balance   int64      `json:"balance"`
	Funds     []CardFund `json:"funds"`
	Archived  bool       `json:"archived"`
	SortOrder int        `json:"sort_order"`
}

// Cards lists every card, archived ones last.
func (s *Store) Cards() ([]Card, error) {
	rows, err := s.db.Query(`
		SELECT id, kind, bank, name, last4, network, archived, sort_order
		FROM cards
		ORDER BY archived, sort_order, id`)
	if err != nil {
		return nil, fmt.Errorf("list cards: %w", err)
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var card Card
		if err := rows.Scan(&card.ID, &card.Kind, &card.Bank, &card.Name, &card.Last4, &card.Network, &card.Archived, &card.SortOrder); err != nil {
			return nil, err
		}
		if err := s.attachFunds(&card); err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	return out, rows.Err()
}

// CreateCard inserts a card. Balance is the current amount in Currency (CNY if empty).
func (s *Store) CreateCard(in Card) (Card, error) {
	if err := normalizeCard(&in); err != nil {
		return Card{}, err
	}
	res, err := s.db.Exec(`
		INSERT INTO cards (kind, bank, name, last4, network, balance_offset, archived, sort_order)
		VALUES (?, ?, ?, ?, ?, 0, 0, COALESCE((SELECT MAX(sort_order) + 1 FROM cards), 0))`,
		in.Kind, in.Bank, in.Name, in.Last4, in.Network)
	if err != nil {
		return Card{}, fmt.Errorf("create card: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Card{}, err
	}
	if err := s.applyCardFunds(id, in, &in.Balance); err != nil {
		return Card{}, err
	}
	return s.Card(id)
}

// UpdateCard replaces the editable fields. A nil balance leaves funds unchanged;
// a value sets Currency (or CNY) to that amount.
func (s *Store) UpdateCard(id int64, in Card, balance *int64) (Card, error) {
	if err := normalizeCard(&in); err != nil {
		return Card{}, err
	}
	res, err := s.db.Exec(
		`UPDATE cards SET kind = ?, bank = ?, name = ?, last4 = ?, network = ?, archived = ?, sort_order = ? WHERE id = ?`,
		in.Kind, in.Bank, in.Name, in.Last4, in.Network, boolInt(in.Archived), in.SortOrder, id)
	if err != nil {
		return Card{}, fmt.Errorf("update card: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Card{}, ErrNotFound
	}
	if err := s.applyCardFunds(id, in, balance); err != nil {
		return Card{}, err
	}
	return s.Card(id)
}

// ReorderCards writes the display order. The ids must be exactly the current
// set, with no extras, gaps, or duplicates.
func (s *Store) ReorderCards(ids []int64) error {
	current, err := s.Cards()
	if err != nil {
		return err
	}
	want := map[int64]struct{}{}
	for _, card := range current {
		want[card.ID] = struct{}{}
	}
	if err := exactIDs(ids, want, "卡片顺序与现有银行卡不一致"); err != nil {
		return err
	}
	return s.inTx(func(tx *sql.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(`UPDATE cards SET sort_order = ? WHERE id = ?`, i, id); err != nil {
				return fmt.Errorf("reorder cards: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) applyCardFunds(id int64, in Card, balance *int64) error {
	if len(in.Funds) > 0 {
		keep := map[string]bool{}
		for _, fund := range in.Funds {
			if err := s.SetCardBalance(id, fund.Currency, fund.Balance); err != nil {
				return err
			}
			keep[normalizeCurrency(fund.Currency)] = true
		}
		return s.dropUnusedFunds(id, keep)
	}
	if balance == nil {
		return nil
	}
	return s.SetCardBalance(id, in.Currency, *balance)
}

func (s *Store) dropUnusedFunds(id int64, keep map[string]bool) error {
	used, err := s.cardCurrencies(id)
	if err != nil {
		return err
	}
	for currency := range used {
		if !keep[currency] {
			return invalid("这张卡上已有该货币的账目，不能删除")
		}
	}
	rows, err := s.db.Query(`SELECT currency FROM card_funds WHERE card_id = ?`, id)
	if err != nil {
		return fmt.Errorf("list card funds: %w", err)
	}
	defer rows.Close()
	drop := []string{}
	for rows.Next() {
		var currency string
		if err := rows.Scan(&currency); err != nil {
			return err
		}
		if !keep[currency] {
			drop = append(drop, currency)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, currency := range drop {
		if _, err := s.db.Exec(`DELETE FROM card_funds WHERE card_id = ? AND currency = ?`, id, currency); err != nil {
			return fmt.Errorf("drop card fund: %w", err)
		}
	}
	return nil
}

// SetCardBalance makes the current amount of one currency on a card equal to cents.
func (s *Store) SetCardBalance(id int64, currency string, cents int64) error {
	currency = normalizeCurrency(currency)
	if err := checkCurrency(currency); err != nil {
		return err
	}
	if err := checkCardBalance(cents); err != nil {
		return err
	}
	ok, err := s.exists(`SELECT 1 FROM cards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	net, err := s.cardNet(id, currency)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO card_funds (card_id, currency, balance_offset) VALUES (?, ?, ?)
		ON CONFLICT(card_id, currency) DO UPDATE SET balance_offset = excluded.balance_offset`,
		id, currency, cents-net)
	if err != nil {
		return fmt.Errorf("set card balance: %w", err)
	}
	return nil
}

// DeleteCard removes an unused card.
func (s *Store) DeleteCard(id int64) error {
	used, err := s.exists(`SELECT 1 FROM transactions WHERE card_id = ? OR to_card_id = ? LIMIT 1`, id, id)
	if err != nil {
		return err
	}
	if used {
		return ErrInUse
	}
	res, err := s.db.Exec(`DELETE FROM cards WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete card: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Card reads one card with its current funds.
func (s *Store) Card(id int64) (Card, error) {
	card := Card{}
	err := s.db.QueryRow(`
		SELECT id, kind, bank, name, last4, network, archived, sort_order
		FROM cards WHERE id = ?`, id).
		Scan(&card.ID, &card.Kind, &card.Bank, &card.Name, &card.Last4, &card.Network, &card.Archived, &card.SortOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return Card{}, ErrNotFound
	}
	if err != nil {
		return Card{}, err
	}
	if err := s.attachFunds(&card); err != nil {
		return Card{}, err
	}
	return card, nil
}

func (s *Store) attachFunds(card *Card) error {
	offsets := map[string]int64{}
	rows, err := s.db.Query(`SELECT currency, balance_offset FROM card_funds WHERE card_id = ?`, card.ID)
	if err != nil {
		return fmt.Errorf("list card funds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var currency string
		var offset int64
		if err := rows.Scan(&currency, &offset); err != nil {
			return err
		}
		offsets[currency] = offset
	}
	if err := rows.Err(); err != nil {
		return err
	}
	used, err := s.cardCurrencies(card.ID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	funds := []CardFund{}
	for _, currency := range Currencies {
		_, hasOffset := offsets[currency]
		if !hasOffset && !used[currency] {
			continue
		}
		net, err := s.cardNet(card.ID, currency)
		if err != nil {
			return err
		}
		funds = append(funds, CardFund{Currency: currency, Balance: offsets[currency] + net})
		seen[currency] = true
	}
	if len(funds) == 0 {
		funds = []CardFund{{Currency: CurrencyCNY, Balance: 0}}
	}
	card.Funds = funds
	card.Balance = fundAmount(funds, CurrencyCNY)
	return nil
}

func (s *Store) cardCurrencies(id int64) (map[string]bool, error) {
	rows, err := s.db.Query(`
		SELECT currency FROM transactions WHERE card_id = ? OR to_card_id = ?
		UNION
		SELECT to_currency FROM transactions WHERE kind = 'exchange' AND card_id = ? AND to_currency != ''`,
		id, id, id)
	if err != nil {
		return nil, fmt.Errorf("card currencies: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var currency string
		if err := rows.Scan(&currency); err != nil {
			return nil, err
		}
		if currency != "" {
			out[currency] = true
		}
	}
	return out, rows.Err()
}

func (s *Store) cardNet(id int64, currency string) (int64, error) {
	var net int64
	err := s.db.QueryRow(`
		SELECT COALESCE(SUM(CASE
		  WHEN kind = 'income' AND card_id = ? AND currency = ? THEN amount
		  WHEN kind = 'expense' AND card_id = ? AND currency = ? THEN -amount
		  WHEN kind = 'transfer' AND to_card_id = ? AND currency = ? THEN amount
		  WHEN kind = 'transfer' AND card_id = ? AND currency = ? THEN -amount
		  WHEN kind = 'exchange' AND card_id = ? AND to_currency = ? THEN to_amount
		  WHEN kind = 'exchange' AND card_id = ? AND currency = ? THEN -amount
		  ELSE 0
		END), 0)
		FROM transactions`,
		id, currency, id, currency, id, currency, id, currency, id, currency, id, currency).Scan(&net)
	if err != nil {
		return 0, fmt.Errorf("card net: %w", err)
	}
	return net, nil
}

func fundAmount(funds []CardFund, currency string) int64 {
	for _, fund := range funds {
		if fund.Currency == currency {
			return fund.Balance
		}
	}
	return 0
}

func normalizeCard(c *Card) error {
	if c.Kind != CardDebit && c.Kind != CardCredit {
		return invalid("银行卡类型只能是储蓄卡或信用卡")
	}
	c.Bank = strings.TrimSpace(c.Bank)
	c.Name = strings.TrimSpace(c.Name)
	c.Last4 = strings.TrimSpace(c.Last4)
	if c.Bank == "" {
		return invalid("请填写银行")
	}
	if utf8.RuneCountInString(c.Bank) > 16 {
		return invalid("银行名称最多 16 个字")
	}
	if utf8.RuneCountInString(c.Name) > 24 {
		return invalid("卡名最多 24 个字")
	}
	if len(c.Last4) != 4 || !isDigits(c.Last4) {
		return invalid("请填写四位尾号")
	}
	c.Network = strings.TrimSpace(c.Network)
	return checkNetwork(c.Network)
}

func checkNetwork(code string) error {
	if code == "" {
		return nil
	}
	for _, item := range networks {
		if item == code {
			return nil
		}
	}
	return invalid("不支持的卡组织")
}

func checkCardBalance(cents int64) error {
	if cents < -money.MaxCents || cents > money.MaxCents {
		return invalid("金额过大")
	}
	return nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (s *Store) cardExists(id int64) error {
	if id <= 0 {
		return invalid("请选择银行卡")
	}
	ok, err := s.exists(`SELECT 1 FROM cards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if !ok {
		return invalid("银行卡不存在")
	}
	return nil
}
