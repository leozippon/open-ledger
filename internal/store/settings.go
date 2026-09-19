package store

import (
	"errors"

	"ledger/internal/money"
)

// Budget is the default activity's cap. The family balance card no longer shows it.
func (s *Store) Budget() (int64, error) {
	item, err := s.DefaultActivity()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return item.Budget, nil
}

// SetBudget writes the default activity's cap; 0 clears it.
func (s *Store) SetBudget(cents int64) error {
	if cents < 0 {
		return invalid("预算不能为负数")
	}
	if cents > money.MaxCents {
		return invalid("金额过大")
	}
	item, err := s.DefaultActivity()
	if err != nil {
		return err
	}
	item.Budget = cents
	_, err = s.UpdateActivity(item.ID, item)
	return err
}

// FamilyBalances sums each currency across every card. Currencies are not converted.
func (s *Store) FamilyBalances() ([]CurrencyTotal, error) {
	cards, err := s.Cards()
	if err != nil {
		return nil, err
	}
	by := map[string]int64{}
	for _, card := range cards {
		for _, fund := range card.Funds {
			by[fund.Currency] += fund.Balance
		}
	}
	return sortCurrencyTotals(by), nil
}

// FamilyBalance is the family's CNY total. Other currencies live in FamilyBalances.
func (s *Store) FamilyBalance() (int64, error) {
	totals, err := s.FamilyBalances()
	if err != nil {
		return 0, err
	}
	return currencyAmount(totals, CurrencyCNY), nil
}

func sortCurrencyTotals(by map[string]int64) []CurrencyTotal {
	out := []CurrencyTotal{}
	for _, code := range Currencies {
		amount, ok := by[code]
		if !ok || amount == 0 {
			continue
		}
		out = append(out, CurrencyTotal{Currency: code, Amount: amount})
	}
	if len(out) == 0 {
		return []CurrencyTotal{{Currency: CurrencyCNY, Amount: 0}}
	}
	return out
}

func currencyAmount(totals []CurrencyTotal, code string) int64 {
	for _, item := range totals {
		if item.Currency == code {
			return item.Amount
		}
	}
	return 0
}
