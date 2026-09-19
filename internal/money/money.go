// Package money converts between human amounts in yuan and the integer cents
// used everywhere else in the ledger.
package money

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// MaxCents caps a single amount at 100 million yuan, which keeps every sum a
// personal ledger can produce far away from int64 overflow.
const MaxCents int64 = 10_000_000_000

// ParseYuan turns user input such as "12", "12.5" or "12.50" into cents.
// It rejects empty input, signs, thousand separators, more than two decimals
// and non-positive results.
func ParseYuan(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("请输入金额")
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if !isDigits(intPart) || (hasDot && !isDigits(fracPart)) {
		return 0, errors.New("金额格式不正确")
	}
	if len(fracPart) > 2 {
		return 0, errors.New("金额最多保留两位小数")
	}
	if len(intPart) > 11 {
		return 0, errors.New("金额过大")
	}
	yuan, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, errors.New("金额格式不正确")
	}
	cents := yuan * 100
	switch len(fracPart) {
	case 1:
		cents += int64(fracPart[0]-'0') * 10
	case 2:
		cents += int64(fracPart[0]-'0')*10 + int64(fracPart[1]-'0')
	}
	return cents, Validate(cents)
}

// Validate checks that cents is a usable amount for a transaction.
func Validate(cents int64) error {
	if cents <= 0 {
		return errors.New("金额必须大于 0")
	}
	if cents > MaxCents {
		return errors.New("金额过大")
	}
	return nil
}

// FormatYuan renders cents as a plain decimal string with two decimals.
func FormatYuan(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return sign + strconv.FormatInt(cents/100, 10) + "." + twoDigits(cents%100)
}

// Cents is an amount on the wire. The web app always sends integer cents; a
// decimal yuan string is also accepted so the API stays usable by hand.
type Cents int64

func (c *Cents) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		v, err := ParseYuan(s)
		if err != nil {
			return err
		}
		*c = Cents(v)
		return nil
	}
	var v int64
	if err := json.Unmarshal(data, &v); err != nil {
		return errors.New("金额格式不正确")
	}
	*c = Cents(v)
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

func twoDigits(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
