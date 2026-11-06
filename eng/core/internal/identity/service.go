// Package identity owns accounts, tenants, memberships and sessions:
// registration, login with argon2id, 10 minute EdDSA access tokens, opaque
// refresh tokens rotated on every use with family revocation on reuse, and
// roles per tenant. See README.md.
package identity

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/cuit"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// Roles (enum role in migration 00001).
const (
	RoleOwner      = "owner"
	RoleApprover   = "approver"
	RoleViewer     = "viewer"
	RoleOperator   = "operator"
	RoleAccountant = "accountant"
)

// Errors the HTTP layer maps to problems.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidRefresh     = errors.New("invalid refresh token")
	ErrRefreshReused      = errors.New("refresh token reused; session revoked")
	ErrEmailTaken         = errors.New("email already registered")
	ErrSlugTaken          = errors.New("tenant slug already taken")
	ErrCuitTaken          = errors.New("cuit already registered")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrInvalidCuit        = errors.New("invalid cuit check digit")
	ErrForbidden          = errors.New("forbidden")
	ErrAlreadyMember      = errors.New("already a member")
)

// Service implements the identity use cases.
type Service struct {
	DB      *db.DB
	Signer  *Signer
	Keyring *crypto.Keyring
	Argon   Argon2Params
	now     func() time.Time
	dummy   string // hash compared when the email is unknown (constant time login)
}

// NewService builds the service.
func NewService(d *db.DB, s *Signer, k *crypto.Keyring, p Argon2Params) (*Service, error) {
	dummy, err := HashPassword(p, "dummy password for unknown users")
	if err != nil {
		return nil, err
	}
	return &Service{DB: d, Signer: s, Keyring: k, Argon: p, now: time.Now, dummy: dummy}, nil
}

// RegisterInput is a new account with its first tenant.
type RegisterInput struct {
	Email, Password, TenantName, TenantSlug string
	Cuit                                    string // optional, 11 digits
}

// Register creates the user, the tenant, the owner membership and the
// tenant data key in one transaction.
func (s *Service) Register(ctx context.Context, in RegisterInput) (user, tenant uuid.UUID, err error) {
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	var cuitNum *int64
	if in.Cuit != "" {
		n, err := cuit.Parse(in.Cuit)
		if err != nil {
			return uuid.Nil, uuid.Nil, ErrInvalidCuit
		}
		cuitNum = &n
	}
	hash, err := HashPassword(s.Argon, in.Password)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if user, err = q.CreateUser(ctx, store.CreateUserParams{Email: email, PasswordHash: &hash}); err != nil {
			return mapUnique(err)
		}
		if tenant, err = q.CreateTenant(ctx, store.CreateTenantParams{Slug: in.TenantSlug, Name: in.TenantName, Cuit: cuitNum}); err != nil {
			return mapUnique(err)
		}
		if err := q.CreateMembership(ctx, store.CreateMembershipParams{UserID: user, TenantID: tenant, Role: store.RoleOwner}); err != nil {
			return err
		}
		if err := s.Keyring.EnsureTenantKey(ctx, tx, tenant); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.register", Subject: "tenants:" + tenant.String(), TenantID: tenant, UserID: user})
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return user, tenant, nil
}

// Session is what login and refresh hand back.
type Session struct {
	Access    string
	Refresh   string
	Principal Principal
}

// Login checks the password and opens a session (new refresh family) on
// tenant, or on the oldest membership when tenant is nil.
func (s *Service) Login(ctx context.Context, email, password string, tenant *uuid.UUID) (Session, error) {
	email = strings.TrimSpace(email)
	var out Session
	var failed bool
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		u, err := q.GetUserForLogin(ctx, email)
		hash := s.dummy
		if err == nil && u.PasswordHash != nil {
			hash = *u.PasswordHash
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		ok, verr := VerifyPassword(hash, password)
		if verr != nil || !ok || err != nil || u.PasswordHash == nil {
			failed = true
			subject := "users:unknown"
			if err == nil {
				subject = "users:" + u.ID.String()
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.login_failed", Subject: subject, UserID: u.ID})
		}
		p, err := s.pickTenant(ctx, q, u.ID, tenant)
		if err != nil {
			failed = true
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.login_failed", Subject: "users:" + u.ID.String(), UserID: u.ID,
				Detail: map[string]any{"reason": "no membership"}})
		}
		out, err = s.openSession(ctx, q, p, uuid.New())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.login", Subject: "users:" + u.ID.String(), TenantID: p.TenantID, UserID: p.UserID})
	})
	if err != nil {
		return Session{}, err
	}
	if failed {
		return Session{}, ErrInvalidCredentials
	}
	return out, nil
}

func (s *Service) pickTenant(ctx context.Context, q *store.Queries, user uuid.UUID, want *uuid.UUID) (Principal, error) {
	ms, err := q.ListMembershipsForUser(ctx, user)
	if err != nil {
		return Principal{}, err
	}
	for _, m := range ms {
		if want == nil || m.TenantID == *want {
			return Principal{UserID: user, TenantID: m.TenantID, Role: string(m.Role)}, nil
		}
	}
	return Principal{}, ErrInvalidCredentials
}

func (s *Service) openSession(ctx context.Context, q *store.Queries, p Principal, family uuid.UUID) (Session, error) {
	refresh, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	tenant := p.TenantID
	if err := q.InsertRefreshToken(ctx, store.InsertRefreshTokenParams{
		UserID: p.UserID, TenantID: &tenant, FamilyID: family, TokenHash: hash, ExpiresAt: s.now().Add(RefreshTTL),
	}); err != nil {
		return Session{}, err
	}
	access, err := s.Signer.Issue(p)
	if err != nil {
		return Session{}, err
	}
	return Session{Access: access, Refresh: refresh, Principal: p}, nil
}

// Refresh rotates a refresh token. Presenting a token that was already
// rotated revokes every token of its family and fails: whoever holds the
// newest one (the legitimate client or the thief) has to log in again.
func (s *Service) Refresh(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrInvalidRefresh
	}
	var out Session
	var outcome error
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		rt, err := q.GetRefreshTokenForUpdate(ctx, hashRefresh(token))
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ErrInvalidRefresh
			return nil
		}
		if err != nil {
			return err
		}
		subject := "refresh_families:" + rt.FamilyID.String()
		tenant := uuid.Nil
		if rt.TenantID != nil {
			tenant = *rt.TenantID
		}
		switch {
		case rt.RevokedAt != nil:
			outcome = ErrInvalidRefresh
			return nil
		case rt.UsedAt != nil:
			// Committed on purpose: the revocation must survive the 401.
			outcome = ErrRefreshReused
			if _, err := q.RevokeFamily(ctx, rt.FamilyID); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.refresh_reuse", Subject: subject, TenantID: tenant, UserID: rt.UserID})
		case !s.now().Before(rt.ExpiresAt) || rt.TenantID == nil:
			outcome = ErrInvalidRefresh
			return nil
		}
		role, err := q.GetMembershipRole(ctx, store.GetMembershipRoleParams{UserID: rt.UserID, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) {
			// Membership removed since login: the session ends.
			outcome = ErrInvalidRefresh
			_, err = q.RevokeFamily(ctx, rt.FamilyID)
			return err
		}
		if err != nil {
			return err
		}
		if err := q.MarkRefreshTokenUsed(ctx, rt.ID); err != nil {
			return err
		}
		out, err = s.openSession(ctx, q, Principal{UserID: rt.UserID, TenantID: tenant, Role: string(role)}, rt.FamilyID)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if outcome != nil {
		return Session{}, outcome
	}
	return out, nil
}

// Logout revokes the family of token. Unknown tokens are ignored.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		rt, err := q.GetRefreshTokenForUpdate(ctx, hashRefresh(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := q.RevokeFamily(ctx, rt.FamilyID); err != nil {
			return err
		}
		tenant := uuid.Nil
		if rt.TenantID != nil {
			tenant = *rt.TenantID
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.logout", Subject: "refresh_families:" + rt.FamilyID.String(), TenantID: tenant, UserID: rt.UserID})
	})
}

// MeView is the caller with every membership.
type MeView struct {
	Principal   Principal
	Email       string
	Memberships []store.ListMembershipsForUserRow
}

// Me returns the caller. The memberships span tenants, so it reads them in
// a system transaction filtered by the caller's own user id.
func (s *Service) Me(ctx context.Context, p Principal) (MeView, error) {
	v := MeView{Principal: p}
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if v.Email, err = q.GetUserEmail(ctx, p.UserID); err != nil {
			return err
		}
		v.Memberships, err = q.ListMembershipsForUser(ctx, p.UserID)
		return err
	})
	return v, err
}

// ListMembers returns the members of the caller's tenant (under RLS).
func (s *Service) ListMembers(ctx context.Context) ([]store.ListMembersRow, error) {
	scope, ok := db.ScopeFrom(ctx)
	if !ok {
		return nil, db.ErrNoScope
	}
	var rows []store.ListMembersRow
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var err error
		rows, err = store.New(tx).ListMembers(ctx, scope.TenantID)
		return err
	})
	return rows, err
}

// AddMember gives email the role in the caller's tenant. Owners and
// operators may add members; only an operator grants operator or accountant.
func (s *Service) AddMember(ctx context.Context, p Principal, email, role string) (store.ListMembersRow, error) {
	if !CanManageMembers(p.Role) || ((role == RoleOperator || role == RoleAccountant) && p.Role != RoleOperator) {
		return store.ListMembersRow{}, ErrForbidden
	}
	if !validRole(role) {
		return store.ListMembersRow{}, fmt.Errorf("unknown role %q", role)
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return store.ListMembersRow{}, err
	}
	// users has no tenant and tramo_app cannot insert into it: the user row
	// is ensured in a system transaction, the membership under RLS.
	var user uuid.UUID
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		var err error
		user, err = store.New(tx).EnsureUser(ctx, email)
		return err
	})
	if err != nil {
		return store.ListMembersRow{}, err
	}
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		if err := store.New(tx).CreateMembership(ctx, store.CreateMembershipParams{UserID: user, TenantID: p.TenantID, Role: store.Role(role)}); err != nil {
			return mapUnique(err)
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.member_added", Subject: "users:" + user.String(), Detail: map[string]any{"role": role}})
	})
	if err != nil {
		return store.ListMembersRow{}, err
	}
	return store.ListMembersRow{ID: user, Email: email, Role: store.Role(role)}, nil
}

// CanManageMembers reports whether role may add people to a tenant.
func CanManageMembers(role string) bool { return role == RoleOwner || role == RoleOperator }

func validRole(r string) bool {
	switch r {
	case RoleOwner, RoleApprover, RoleViewer, RoleOperator, RoleAccountant:
		return true
	}
	return false
}

func normalizeEmail(s string) (string, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil || a.Name != "" || a.Address != strings.TrimSpace(s) {
		return "", ErrInvalidEmail
	}
	return a.Address, nil
}

func mapUnique(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23505" {
		return err
	}
	switch pg.ConstraintName {
	case "users_email_key":
		return ErrEmailTaken
	case "tenants_slug_key":
		return ErrSlugTaken
	case "tenants_cuit_key":
		return ErrCuitTaken
	case "memberships_pkey":
		return ErrAlreadyMember
	}
	return err
}
