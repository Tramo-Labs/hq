// Package money holds amounts as integer cents. Parsing is strict: the caller
// states the decimal style of the source, and anything that does not fit it
// exactly is an error. A bank export never gets a best guess.
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Amount is a signed amount in cents of the statement currency.
type Amount int64

// Style is the decimal convention of a source.
type Style int

const (
	// DecimalComma is the Argentine convention: 1.234.567,89
	DecimalComma Style = iota
	// DecimalDot is the API convention: 1234567.89 (thousands separator ',' allowed)
	DecimalDot
)

// ErrEmpty is returned when the cell holds no amount at all.
var ErrEmpty = errors.New("empty amount")

// Parse reads an amount in the given style. It accepts a leading "$" or
// currency code, a leading or trailing minus and accounting parentheses.
// It rejects more than two decimals, misplaced thousands separators and any
// stray character.
func Parse(s string, style Style) (Amount, error) {
	orig := s
	s = strings.TrimSpace(s)
	for _, p := range []string{"ARS", "U$S", "USD", "$"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	if s == "" {
		return 0, ErrEmpty
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, strings.TrimSpace(s[1:len(s)-1])
	}
	switch {
	case strings.HasPrefix(s, "-"):
		if neg {
			return 0, fmt.Errorf("amount %q: double negative", orig)
		}
		neg, s = true, strings.TrimSpace(s[1:])
	case strings.HasSuffix(s, "-"):
		if neg {
			return 0, fmt.Errorf("amount %q: double negative", orig)
		}
		neg, s = true, strings.TrimSpace(s[:len(s)-1])
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}
	dec, thou := ",", "."
	if style == DecimalDot {
		dec, thou = ".", ","
	}
	intPart, fracPart := s, "00"
	if i := strings.LastIndex(s, dec); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
		if len(fracPart) == 0 || len(fracPart) > 2 || !digits(fracPart) {
			return 0, fmt.Errorf("amount %q: bad decimals", orig)
		}
		if len(fracPart) == 1 {
			fracPart += "0"
		}
	}
	if strings.Contains(intPart, thou) {
		groups := strings.Split(intPart, thou)
		if len(groups[0]) == 0 || len(groups[0]) > 3 {
			return 0, fmt.Errorf("amount %q: bad thousands grouping", orig)
		}
		for _, g := range groups[1:] {
			if len(g) != 3 {
				return 0, fmt.Errorf("amount %q: bad thousands grouping", orig)
			}
		}
		intPart = strings.Join(groups, "")
	}
	if intPart == "" {
		intPart = "0"
	}
	if !digits(intPart) {
		return 0, fmt.Errorf("amount %q: not a number", orig)
	}
	if len(intPart) > 15 {
		return 0, fmt.Errorf("amount %q: out of range", orig)
	}
	n, err := strconv.ParseInt(intPart+fracPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", orig, err)
	}
	if neg {
		n = -n
	}
	return Amount(n), nil
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// String renders the amount as 1234.56 (dot decimal, no grouping).
func (a Amount) String() string {
	sign := ""
	n := int64(a)
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// Abs returns the absolute value.
func (a Amount) Abs() Amount {
	if a < 0 {
		return -a
	}
	return a
}
