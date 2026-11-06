package text

import (
	"errors"
	"strings"
	"testing"
)

// Invisible characters are written as escapes so a review can see them.
func TestHasLetterOrNumber(t *testing.T) {
	for _, s := range []string{"", "   ", "\u200b", "\u200b\u200b", "\u3164\u3164", "\u2800", "\u202e", "\u00a0",
		"...", "★", "\u200d\ufeff"} {
		if HasLetterOrNumber(s) {
			t.Errorf("%q counts as a reason", s)
		}
	}
	for _, s := range []string{"x", "7", "retiro de socio", "\u200bok", "ñandú", "給料"} {
		if !HasLetterOrNumber(s) {
			t.Errorf("%q does not count as a reason", s)
		}
	}
}

func TestName(t *testing.T) {
	for _, s := range []string{"", "  ", "\u200b\u200b", "\u3164", "\u3164\u3164", "Cuenta \u202elaicnes",
		"Cuenta\nsueldos", "Cuenta\u2028sueldos", "...", strings.Repeat("a", 121)} {
		if _, err := Name(s, 120); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%q accepted as a name", s)
		}
	}
	if got, err := Name("  Cuenta sueldos  ", 120); err != nil || got != "Cuenta sueldos" {
		t.Errorf("trimmed name: %q %v", got, err)
	}
	if got, err := Name(strings.Repeat("ñ", 120), 120); err != nil || got == "" {
		t.Errorf("120 characters: %v", err)
	}
}
