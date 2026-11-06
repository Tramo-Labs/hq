// Package mercadopago connects a tenant's Mercado Pago account by OAuth with
// PKCE (D-22, ADR 0010): the owner authorizes, the portal's return page
// hands back code and state, the tokens are kept encrypted by the tenant's
// data key and renewed before they expire. When Mercado Pago stops
// accepting them the connection waits for the owner to reconnect. Fetching
// the settlement report for datos comes next. See README.md.
package mercadopago

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// Connection status (api/openapi.yaml, MercadoPagoConnection).
const (
	Disconnected   = "disconnected"
	Connected      = "connected"
	NeedsReconnect = "needs_reconnect"
)

// Error codes kept for the GET, never Mercado Pago's text.
const (
	CodeRevoked     = "revoked"
	CodeUnavailable = "mp_unavailable"
)

// StateTTL is how long an authorization request is accepted; Mercado
// Pago's own code lasts 10 minutes too.
const StateTTL = 10 * time.Minute

// RefreshBefore is how long before expiry a token is renewed. Mercado
// Pago's access tokens last 180 days; renewing a month early leaves many
// daily attempts if Mercado Pago is down for a while.
const RefreshBefore = 30 * 24 * time.Hour

const tokenPurpose = "mercadopago.token"

var (
	ErrForbidden     = errors.New("mercadopago: your role cannot do this")
	ErrNotConfigured = errors.New("mercadopago: no app configured")
	ErrStateInvalid  = errors.New("mercadopago: unknown state")
	ErrStateNotYours = errors.New("mercadopago: state started by someone else")
	ErrStateExpired  = errors.New("mercadopago: state expired")
	ErrStateUsed     = errors.New("mercadopago: state already used")
	ErrCodeRejected  = errors.New("mercadopago: Mercado Pago rejected the code")
	ErrMPUnavailable = errors.New("mercadopago: Mercado Pago unavailable")
)

// Service is the Mercado Pago connection of tenants.
type Service struct {
	DB      *db.DB
	Keyring *crypto.Keyring
	Config  Config
	HTTP    *http.Client // nil: a client with a 15 second timeout
	Now     func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) client() *client { return newClient(s.Config, s.HTTP) }

// Status is what GET /v1/integrations/mercadopago shows. Never a token.
type Status struct {
	Configured     bool
	Status         string
	MPUserID       string
	ConnectedAt    *time.Time
	ConnectedBy    *uuid.UUID
	TokenExpiresAt *time.Time
	LastRefreshAt  *time.Time
	LastErrorCode  string
	LastErrorAt    *time.Time
	RevokeInMP     bool
}

func role(ctx context.Context, q *store.Queries) (string, error) {
	r, err := q.MemberRole(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return r, err
}

func mustBe(ctx context.Context, q *store.Queries, allowed ...string) error {
	r, err := role(ctx, q)
	if err != nil {
		return err
	}
	for _, a := range allowed {
		if r == a {
			return nil
		}
	}
	return ErrForbidden
}

func (s *Service) status(ctx context.Context, q *store.Queries) (Status, error) {
	st := Status{Configured: s.Config.Configured(), Status: Disconnected}
	c, err := q.Connection(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Status = c.Status
	at := c.ConnectedAt
	st.ConnectedAt, st.ConnectedBy, st.TokenExpiresAt = &at, c.ConnectedBy, c.TokenExpiresAt
	st.LastRefreshAt, st.LastErrorAt = c.LastRefreshAt, c.LastErrorAt
	if c.MpUserID != nil {
		st.MPUserID = *c.MpUserID
	}
	if c.LastErrorCode != nil {
		st.LastErrorCode = *c.LastErrorCode
	}
	return st, nil
}

// Get returns the tenant's connection (owner, accountant, operator).
func (s *Service) Get(ctx context.Context) (Status, error) {
	var st Status
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := mustBe(ctx, q, "owner", "accountant", "operator"); err != nil {
			return err
		}
		var err error
		st, err = s.status(ctx, q)
		return err
	})
	return st, err
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func stateHash(state string) []byte {
	sum := sha256.Sum256([]byte(state))
	return sum[:]
}

// Authorize starts a connection (owner): a state and a PKCE verifier,
// stored for StateTTL, and the URL to send the browser to.
func (s *Service) Authorize(ctx context.Context) (string, time.Time, error) {
	var authURL string
	expires := s.now().Add(StateTTL)
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := mustBe(ctx, q, "owner"); err != nil {
			return err
		}
		if !s.Config.Configured() {
			return ErrNotConfigured
		}
		scope, _ := db.ScopeFrom(ctx)
		state := randomString(32)    // 256 bits
		verifier := randomString(64) // 86 characters, within PKCE's 43 to 128
		sealed, err := s.Keyring.Encrypt(ctx, tx, scope.TenantID, []byte(verifier), tokenPurpose)
		if err != nil {
			return err
		}
		if err := q.PurgeAuthRequests(ctx); err != nil {
			return err
		}
		if err := q.InsertAuthRequest(ctx, store.InsertAuthRequestParams{StateHash: stateHash(state),
			Verifier: sealed, ExpiresAt: expires}); err != nil {
			return err
		}
		authURL = s.client().authorizeURL(state, verifier)
		return audit.Record(ctx, tx, audit.Entry{Action: "mercadopago.authorize", Subject: "mercadopago.connections:" + scope.TenantID.String()})
	})
	return authURL, expires, err
}

// Connect finishes a connection (owner) with what Mercado Pago returned.
// The state is locked while the code is exchanged, so two connects with the
// same state cannot both spend it; a refusal stores nothing.
func (s *Service) Connect(ctx context.Context, code, state string) (Status, error) {
	var st Status
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := mustBe(ctx, q, "owner"); err != nil {
			return err
		}
		if !s.Config.Configured() {
			return ErrNotConfigured
		}
		scope, _ := db.ScopeFrom(ctx)
		h := stateHash(state)
		ar, err := q.AuthRequestForUpdate(ctx, h)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Unknown here: maybe another tenant's. Not told apart from
			// "never existed" for a stranger, but it is for its owner below.
			return ErrStateInvalid
		case err != nil:
			return err
		case ar.UserID != scope.UserID:
			return ErrStateNotYours
		case ar.UsedAt != nil:
			return ErrStateUsed
		case !s.now().Before(ar.ExpiresAt):
			return ErrStateExpired
		}
		verifier, err := s.Keyring.Decrypt(ctx, tx, scope.TenantID, ar.Verifier, tokenPurpose)
		if err != nil {
			return err
		}
		t, err := s.client().exchange(ctx, code, string(verifier))
		switch {
		case errors.Is(err, ErrInvalidGrant), errors.Is(err, ErrRejected):
			return ErrCodeRejected
		case err != nil:
			return fmt.Errorf("%w: %v", ErrMPUnavailable, err)
		}
		if err := s.storeTokens(ctx, tx, q, scope.TenantID, t, true, nil); err != nil {
			return err
		}
		if err := q.UseAuthRequest(ctx, h); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "mercadopago.connect", Subject: "mercadopago.connections:" + scope.TenantID.String(),
			Detail: map[string]any{"mp_user_id": t.UserID}}); err != nil {
			return err
		}
		st, err = s.status(ctx, q)
		return err
	})
	return st, err
}

// storeTokens seals and stores tokens. On a renewal without a new refresh
// token the sealed one in place (keep) stays.
func (s *Service) storeTokens(ctx context.Context, tx pgx.Tx, q *store.Queries, tenant uuid.UUID, t Tokens, fresh bool, keep []byte) error {
	access, err := s.Keyring.Encrypt(ctx, tx, tenant, []byte(t.AccessToken), tokenPurpose)
	if err != nil {
		return err
	}
	refresh := keep
	if t.RefreshToken != "" {
		if refresh, err = s.Keyring.Encrypt(ctx, tx, tenant, []byte(t.RefreshToken), tokenPurpose); err != nil {
			return err
		}
	}
	expires := s.now().Add(t.ExpiresIn)
	scope := t.Scope
	if fresh {
		user := t.UserID
		return q.UpsertConnection(ctx, store.UpsertConnectionParams{MpUserID: &user, AccessToken: access,
			RefreshToken: refresh, TokenExpiresAt: &expires, Scope: &scope})
	}
	return q.StoreRefreshed(ctx, store.StoreRefreshedParams{AccessToken: access, RefreshToken: refresh,
		TokenExpiresAt: &expires, Scope: &scope})
}

// Disconnect deletes the tokens here (owner). Mercado Pago documents no
// endpoint to revoke an authorization from the integrator's side, so the
// answer asks the owner to remove the app there too (RevokeInMP).
func (s *Service) Disconnect(ctx context.Context) (Status, error) {
	var st Status
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := mustBe(ctx, q, "owner"); err != nil {
			return err
		}
		n, err := q.DeleteConnection(ctx)
		if err != nil {
			return err
		}
		scope, _ := db.ScopeFrom(ctx)
		if err := audit.Record(ctx, tx, audit.Entry{Action: "mercadopago.disconnect", Subject: "mercadopago.connections:" + scope.TenantID.String()}); err != nil {
			return err
		}
		st, err = s.status(ctx, q)
		st.RevokeInMP = n > 0
		return err
	})
	return st, err
}

// Refresh renews the tenant's tokens when they expire within RefreshBefore
// (a job runs it, under the tenant's scope with no user). invalid_grant
// means the seller revoked the app, changed their password or the
// authorization ended: the tokens are deleted and the connection waits for
// the owner. Any other failure is kept as mp_unavailable and retried by the
// next run. It reports whether tokens were renewed.
func (s *Service) Refresh(ctx context.Context) (bool, error) {
	if !s.Config.Configured() {
		return false, nil
	}
	renewed := false
	var outcome string
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		c, err := q.ConnectionForUpdate(ctx) // one renewal per tenant at a time
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if c.Status != Connected || c.TokenExpiresAt == nil || c.TokenExpiresAt.Sub(s.now()) > RefreshBefore {
			return nil
		}
		scope, _ := db.ScopeFrom(ctx)
		if c.RefreshToken == nil {
			// No refresh token (the app lacks offline_access): it cannot be
			// renewed, the owner reconnects when it expires.
			if !s.now().Before(*c.TokenExpiresAt) {
				outcome = CodeRevoked
				return q.MarkRevoked(ctx)
			}
			return nil
		}
		refresh, err := s.Keyring.Decrypt(ctx, tx, scope.TenantID, c.RefreshToken, tokenPurpose)
		if err != nil {
			return err
		}
		t, err := s.client().refresh(ctx, string(refresh))
		switch {
		case errors.Is(err, ErrInvalidGrant):
			outcome = CodeRevoked
			if err := q.MarkRevoked(ctx); err != nil {
				return err
			}
		case err != nil:
			outcome = CodeUnavailable
			if err := q.MarkUnavailable(ctx); err != nil {
				return err
			}
		default:
			if err := s.storeTokens(ctx, tx, q, scope.TenantID, t, false, c.RefreshToken); err != nil {
				return err
			}
			renewed = true
		}
		action := "mercadopago.refresh"
		if outcome != "" {
			action = "mercadopago.refresh_failed"
		}
		return audit.Record(ctx, tx, audit.Entry{Action: action, Subject: "mercadopago.connections:" + scope.TenantID.String(),
			Detail: map[string]any{"code": outcome}})
	})
	return renewed, err
}
