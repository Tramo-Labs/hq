package identity

import (
	"errors"
	"strings"
	"testing"
)

// The 120 character limit lives in the code too, not only in the spec:
// counted in characters, so 120 accented letters pass and 121 do not.
func TestNameLengthBoundary(t *testing.T) {
	for _, n := range []string{strings.Repeat("a", MaxName), strings.Repeat("ñ", MaxName), "  " + strings.Repeat("a", MaxName) + "  "} {
		if _, err := normalizeName(n); err != nil {
			t.Errorf("%d characters refused: %v", len([]rune(strings.TrimSpace(n))), err)
		}
	}
	for _, n := range []string{strings.Repeat("a", MaxName+1), strings.Repeat("ñ", MaxName+1)} {
		if _, err := normalizeName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%d characters accepted", len([]rune(n)))
		}
	}
}

// Characters that change how a name reads without showing themselves are
// refused: format characters (Cf: zero width space, the bidi controls that
// flip a name in the audit or the members list), line and paragraph
// separators (Zl, Zp). And a name needs something visible.
func TestNameRefusesInvisibleAndBidiCharacters(t *testing.T) {
	for name, s := range map[string]string{
		"zero width space alone": "​",
		"right to left override": "‮oteB",
		"left to right isolate":  "Be⁦to",
		"line separator":         "Be to",
		"paragraph separator":    "Be to",
		"only spaces":            "   ",
		"only combining marks":   "́̈",
		"control":                "Be\x07to",
	} {
		if _, err := normalizeName(s); !errors.Is(err, ErrInvalidName) {
			t.Errorf("%s accepted", name)
		}
	}
	for _, s := range []string{"Ana Pérez", "O'Brien", "José-María", "李小龙", "Ana (contadora)", "Ana 2"} {
		if got, err := normalizeName(s); err != nil || got != s {
			t.Errorf("%q refused: %v", s, err)
		}
	}
}
