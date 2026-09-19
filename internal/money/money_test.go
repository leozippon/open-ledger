package money_test

import (
	"testing"

	"ledger/internal/money"
)

func TestParseYuan(t *testing.T) {
	valid := map[string]int64{
		"12":     1200,
		"12.5":   1250,
		"12.50":  1250,
		"0.01":   1,
		"  8  ":  800,
		"100000": 10000000,
	}
	for input, want := range valid {
		got, err := money.ParseYuan(input)
		if err != nil {
			t.Errorf("ParseYuan(%q) returned error %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("ParseYuan(%q) = %d, want %d", input, got, want)
		}
	}

	invalid := []string{
		"", " ", "0", "0.00", "-5", "+5", "12.345", "12.", ".5", "1,000",
		"12e3", "abc", "١٢", "999999999999",
	}
	for _, input := range invalid {
		if got, err := money.ParseYuan(input); err == nil {
			t.Errorf("ParseYuan(%q) = %d, want error", input, got)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := money.Validate(1); err != nil {
		t.Errorf("Validate(1) = %v, want nil", err)
	}
	for _, cents := range []int64{0, -1, money.MaxCents + 1} {
		if err := money.Validate(cents); err == nil {
			t.Errorf("Validate(%d) = nil, want error", cents)
		}
	}
}

func TestFormatYuan(t *testing.T) {
	cases := map[int64]string{0: "0.00", 5: "0.05", 1250: "12.50", 100000: "1000.00", -1250: "-12.50"}
	for cents, want := range cases {
		if got := money.FormatYuan(cents); got != want {
			t.Errorf("FormatYuan(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestCentsUnmarshal(t *testing.T) {
	var c money.Cents
	if err := c.UnmarshalJSON([]byte(`1250`)); err != nil || c != 1250 {
		t.Fatalf("number form: got %d, err %v", c, err)
	}
	if err := c.UnmarshalJSON([]byte(`"12.5"`)); err != nil || c != 1250 {
		t.Fatalf("string form: got %d, err %v", c, err)
	}
	if err := c.UnmarshalJSON([]byte(`"12.345"`)); err == nil {
		t.Fatal("string form with three decimals should fail")
	}
	if err := c.UnmarshalJSON([]byte(`"x"`)); err == nil {
		t.Fatal("non-numeric string should fail")
	}
}
