package identity

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
)

// MaxName is the longest name, in characters (users.display_name check).
const MaxName = 120

// ErrInvalidName answers a name that is blank after trimming, too long, or
// carries control characters.
var ErrInvalidName = errors.New("invalid name")

// blankLooking are characters that render as blank but are letters or
// symbols to Unicode, so no category rule catches them: the Hangul fillers
// (U+3164, U+115F, U+1160, U+FFA0) and the Braille blank (U+2800).
var blankLooking = map[rune]bool{0x3164: true, 0x115F: true, 0x1160: true, 0xFFA0: true, 0x2800: true}

// normalizeName trims the name and refuses what cannot be shown: nothing
// left, more than MaxName characters, or control characters (line breaks
// included, which would also break a CSV cell or a mail header).
func normalizeName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > MaxName || !utf8.ValidString(s) {
		return "", ErrInvalidName
	}
	visible := false
	for _, r := range s {
		// Control and format characters (Cf: zero width, the bidi controls
		// that flip how a name reads in the audit or the members list),
		// line or paragraph separators, and characters that look blank
		// never belong in a name.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || blankLooking[r] {
			return "", ErrInvalidName
		}
		if unicode.In(r, unicode.L, unicode.N) {
			visible = true
		}
	}
	// A work name has at least one letter or number: punctuation, symbols
	// or emoji alone ("...", "★") are not a name.
	if !visible {
		return "", ErrInvalidName
	}
	return s, nil
}

// optionalName normalizes a name that may be absent.
func optionalName(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	n, err := normalizeName(*s)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// SetName sets the caller's own name. Only the caller's: the user comes
// from the verified token, never from the request. Audited without the
// value (it is personal data).
func (s *Service) SetName(ctx context.Context, p Principal, name string) (MeView, error) {
	n, err := normalizeName(name)
	if err != nil {
		return MeView{}, err
	}
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		if err := store.New(tx).SetUserName(ctx, store.SetUserNameParams{ID: p.UserID, DisplayName: &n}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.name_changed", Subject: "users:" + p.UserID.String(),
			TenantID: p.TenantID, UserID: p.UserID})
	})
	if err != nil {
		return MeView{}, err
	}
	return s.Me(ctx, p)
}
