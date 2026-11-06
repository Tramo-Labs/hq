package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/text"
)

// MaxName is the longest name, in characters (users.display_name check).
const MaxName = 120

// ErrInvalidName answers a name that is blank after trimming, too long, or
// carries control, format or blank looking characters (text.Name).
var ErrInvalidName = errors.New("invalid name")

// normalizeName applies the shared name rule (text.Name): trimmed,
// something visible, at most MaxName characters, nothing invisible (line
// breaks included, which would also break a CSV cell or a mail header).
func normalizeName(s string) (string, error) {
	n, err := text.Name(s, MaxName)
	if err != nil {
		return "", ErrInvalidName
	}
	return n, nil
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
