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
		// that flip how a name reads in the audit or the members list) and
		// line or paragraph separators never belong in a name.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", ErrInvalidName
		}
		if unicode.In(r, unicode.L, unicode.N, unicode.P, unicode.S) {
			visible = true
		}
	}
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
