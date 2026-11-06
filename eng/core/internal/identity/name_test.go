package identity

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/text"
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
		"zero width space alone": "\u200b",
		"right to left override": "\u202eoteB",
		"left to right isolate":  "Be\u2066to",
		"line separator":         "Be\u2028to",
		"paragraph separator":    "Be\u2029to",
		"only spaces":            " \u00a0 ",
		"only combining marks":   "\u0301\u0308",
		// Look blank yet are letters or symbols to Unicode.
		"hangul filler":               "\u3164",
		"hangul choseong filler":      "\u115f",
		"halfwidth hangul filler":     "\uffa0",
		"braille blank":               "\u2800",
		"two fillers":                 "\u3164\u1160",
		"a filler inside a name":      "Ana\u3164P\u00e9rez",
		"braille blank inside a name": "Ana\u2800P\u00e9rez",
		// Something visible but no letter or number.
		"only punctuation": "...",
		"only symbols":     "★☆",
		"control":          "Be\x07to",
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

// One rule list (internal/text): whatever text refuses as invisible,
// identity refuses in a name, a company name and an email.
func TestIdentityUsesTheSharedTextRules(t *testing.T) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		if !text.Invisible(r) || (r >= 0xD800 && r <= 0xDFFF) {
			continue
		}
		s := string(r)
		if _, err := normalizeName("Ana" + s + "Paz"); err == nil {
			t.Fatalf("name with %U accepted", r)
		}
		if _, err := normalizeEmail("ana" + s + "@paz.test"); err == nil {
			t.Fatalf("email with %U accepted", r)
		}
	}
}
