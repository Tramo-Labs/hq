// Package text holds the rules for short texts people type that others will
// read: a note on a decision, the name of an account. One place, so every
// module refuses the same invisible input.
package text

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidName answers a name that is blank, too long, has nothing
// visible, or carries control or bidi characters.
var ErrInvalidName = errors.New("text: invalid name")

// blankLooking are characters that render as blank but are letters or
// symbols to Unicode, so no category rule catches them: the Hangul fillers
// (U+3164, U+115F, U+1160, U+FFA0) and the Braille blank (U+2800).
var blankLooking = map[rune]bool{0x3164: true, 0x115F: true, 0x1160: true, 0xFFA0: true, 0x2800: true}

// Invisible reports a character that changes how text reads without
// showing itself: controls, format characters (Cf: zero width, soft
// hyphen, the bidi controls), line and paragraph separators, and the blank
// looking letters. The one list every module uses: no name, email or note
// field keeps its own.
func Invisible(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || blankLooking[r]
}

// HasLetterOrNumber reports whether s has at least one letter or number
// that shows: zero width spaces, bidi controls, spaces of any kind and the
// blank looking letters do not count. It is what a required note needs,
// so "\u200b" or "\u3164" is no reason.
func HasLetterOrNumber(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.L, unicode.N) && !blankLooking[r] {
			return true
		}
	}
	return false
}

// Name trims s and refuses what cannot be shown as a name: nothing left,
// more than max characters, invalid UTF-8, control or format characters
// (zero width, the bidi controls that flip how the rest of a line reads),
// line or paragraph separators, blank looking letters, or no letter or
// number at all. Identity applies it to a person's and a company's name,
// ledger to an account's.
func Name(s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > max {
		return "", ErrInvalidName
	}
	for _, r := range s {
		if Invisible(r) {
			return "", ErrInvalidName
		}
	}
	if !HasLetterOrNumber(s) {
		return "", ErrInvalidName
	}
	return s, nil
}
