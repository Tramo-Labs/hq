package money

import "testing"

func TestParse(t *testing.T) {
	ok := []struct {
		in    string
		style Style
		want  Amount
	}{
		{"1.234.567,89", DecimalComma, 123456789},
		{"$ 1.234,5", DecimalComma, 123450},
		{"-1.234,56", DecimalComma, -123456},
		{"1.234,56-", DecimalComma, -123456},
		{"(1.234,56)", DecimalComma, -123456},
		{"1234,56", DecimalComma, 123456},
		{"0,01", DecimalComma, 1},
		{"100", DecimalComma, 10000},
		{"1234567.89", DecimalDot, 123456789},
		{"1,234.56", DecimalDot, 123456},
		{"-0.5", DecimalDot, -50},
		{"ARS 10", DecimalDot, 1000},
	}
	for _, c := range ok {
		got, err := Parse(c.in, c.style)
		if err != nil || got != c.want {
			t.Errorf("Parse(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	bad := []struct {
		in    string
		style Style
	}{
		{"", DecimalComma},
		{"1.234.56", DecimalComma}, // dot used as decimal in comma style
		{"1,234", DecimalComma},    // three decimals
		{"12.34,00", DecimalComma}, // bad grouping
		{"1234.567", DecimalDot},   // three decimals
		{"1,23.00", DecimalDot},    // bad grouping
		{"12a,00", DecimalComma},   // stray character
		{"--1,00", DecimalComma},   // double sign
		{"(1,00)-", DecimalComma},  // double sign
		{"1,", DecimalComma},       // empty decimals
		{"99999999999999999", DecimalDot},
	}
	for _, c := range bad {
		if got, err := Parse(c.in, c.style); err == nil {
			t.Errorf("Parse(%q) = %d; want error", c.in, got)
		}
	}
}

func TestString(t *testing.T) {
	for a, want := range map[Amount]string{0: "0.00", 5: "0.05", -123456: "-1234.56"} {
		if a.String() != want {
			t.Errorf("%d.String() = %s; want %s", a, a.String(), want)
		}
	}
}
