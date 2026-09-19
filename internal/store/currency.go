package store

// Supported currencies. Users enter both sides of an exchange; no rates are stored.
const (
	CurrencyCNY = "CNY"
	CurrencyHKD = "HKD"
	CurrencyUSD = "USD"
	CurrencyCAD = "CAD"
	CurrencyTWD = "TWD"
	CurrencyEUR = "EUR"
	CurrencyGBP = "GBP"
	CurrencyJPY = "JPY"
	CurrencyAUD = "AUD"
	CurrencySGD = "SGD"
)

// Currencies is the display order of every currency the ledger accepts.
var Currencies = []string{
	CurrencyCNY, CurrencyHKD, CurrencyUSD, CurrencyCAD, CurrencyTWD,
	CurrencyEUR, CurrencyGBP, CurrencyJPY, CurrencyAUD, CurrencySGD,
}

// CurrencyTotal is one currency's running amount.
type CurrencyTotal struct {
	Currency string `json:"currency"`
	Amount   int64  `json:"amount"`
}

// CurrencyFlow is one currency's running balance plus this month's income and expense.
type CurrencyFlow struct {
	Currency string `json:"currency"`
	Balance  int64  `json:"balance"`
	Income   int64  `json:"income"`
	Expense  int64  `json:"expense"`
}

func checkCurrency(code string) error {
	if !knownCurrency(code) {
		return invalid("不支持的货币")
	}
	return nil
}

func knownCurrency(code string) bool {
	for _, item := range Currencies {
		if item == code {
			return true
		}
	}
	return false
}

func normalizeCurrency(code string) string {
	if code == "" {
		return CurrencyCNY
	}
	return code
}
