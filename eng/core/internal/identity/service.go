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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/cuit"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	mailer "github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
	"github.com/Tramo-Labs/hq/eng/core/internal/text"
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
	ErrInvalidTenantName  = errors.New("invalid company name")
	ErrForbidden          = errors.New("forbidden")
	ErrAlreadyMember      = errors.New("already a member")
	// ErrPasswordRequired: accepting an invitation without a password, by
	// a person who has none yet.
	ErrPasswordRequired = errors.New("a password is required to accept")
	// ErrNotAMember answers a login with the right password on a tenant
	// the user does not belong to (F18). Only reachable with the password.
	ErrNotAMember = errors.New("not a member of that tenant")
	// ErrMemberNotFound: the user is not a member of the caller's tenant.
	ErrMemberNotFound = errors.New("member not found")
	// ErrLastOwner: removing this member would leave the tenant without owner.
	ErrLastOwner = errors.New("the tenant needs at least one owner")
)

// Service implements the identity use cases.
type Service struct {
	DB      *db.DB
	Signer  *Signer
	Keyring *crypto.Keyring
	Argon   Argon2Params
	Limiter *ratelimit.Limiter
	Limits  Limits
	// Mailer delivers invitations; nil logs that none was sent.
	Mailer mailer.Sender
	// AppBaseURL is the web app's origin, for invitation links.
	AppBaseURL string
	// Enqueue puts a job on the queue (River, jobs.Insert in the api).
	// Magic links are sent from a job so a request never looks the email
	// up (D-17); without it RequestMagicLink fails with ErrNoQueue.
	Enqueue func(ctx context.Context, args river.JobArgs) error
	now     func() time.Time
	dummy   string // hash compared when the email is unknown (constant time login)

	// watch follows failures per email across IPs (D-17). Built on first
	// use from Limits, so a caller may adjust Limits after NewService.
	watchOnce sync.Once
	watch     *attackWatch
}

// NewService builds the service.
func NewService(d *db.DB, s *Signer, k *crypto.Keyring, p Argon2Params) (*Service, error) {
	dummy, err := HashPassword(p, "dummy password for unknown users")
	if err != nil {
		return nil, err
	}
	return &Service{DB: d, Signer: s, Keyring: k, Argon: p, now: time.Now, dummy: dummy,
		Limiter: ratelimit.New(200_000), Limits: DefaultLimits}, nil
}

// RegisterInput is a new account with its first tenant.
type RegisterInput struct {
	Email, Password, TenantName, TenantSlug string
	Cuit                                    string  // optional, 11 digits
	Name                                    *string // optional, the person's name
}

// Register creates the user, the tenant, the owner membership and the
// tenant data key in one transaction.
func (s *Service) Register(ctx context.Context, in RegisterInput) (user, tenant uuid.UUID, err error) {
	if err := s.checkRegister(ctx); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
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
	name, err := optionalName(in.Name)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	// The company name follows the person's name rule (F27): it is shown in
	// the portal, the switcher and the invitation email.
	tenantName, err := normalizeName(in.TenantName)
	if err != nil {
		return uuid.Nil, uuid.Nil, ErrInvalidTenantName
	}
	hash, err := HashPassword(s.Argon, in.Password)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if user, err = q.CreateUser(ctx, store.CreateUserParams{Email: email, PasswordHash: &hash, DisplayName: name}); err != nil {
			return mapUnique(err)
		}
		if tenant, err = q.CreateTenant(ctx, store.CreateTenantParams{Slug: in.TenantSlug, Name: tenantName, Cuit: cuitNum}); err != nil {
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
	email = strings.ToLower(strings.TrimSpace(email))
	notAFailure, err := s.checkLogin(ctx, email)
	if err != nil {
		return Session{}, err
	}
	var out Session
	var failed, notMember bool
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
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
		if errors.Is(err, ErrNotAMember) {
			notMember = true
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.login_failed", Subject: "users:" + u.ID.String(), UserID: u.ID,
				Detail: map[string]any{"reason": "no membership"}})
		}
		if err != nil {
			return err
		}
		out, err = s.openSession(ctx, q, p, uuid.New())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.login", Subject: "users:" + u.ID.String(), TenantID: p.TenantID, UserID: p.UserID})
	})
	if err != nil {
		notAFailure()
		return Session{}, err
	}
	if failed {
		if s.loginFailed(ctx, email) {
			s.auditMagicLinkMode(ctx, email)
		}
		return Session{}, ErrInvalidCredentials
	}
	if notMember {
		// The password was right: not a guess, so no failure is counted.
		notAFailure()
		return Session{}, ErrNotAMember
	}
	s.loginSucceeded(ctx, email)
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
	return Principal{}, ErrNotAMember
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
	return s.rotate(ctx, token, nil)
}

// SwitchTenant moves a session to another of the caller's tenants without
// a password: the refresh token rotates within the same family, now bound
// to tenant, with the role of that membership. A tenant the caller does not
// belong to answers ErrNotAMember and leaves the token as it was. Reuse of
// an old token revokes the family, as on Refresh.
func (s *Service) SwitchTenant(ctx context.Context, token string, tenant uuid.UUID) (Session, error) {
	return s.rotate(ctx, token, &tenant)
}

// rotate is Refresh, or SwitchTenant when target is set.
func (s *Service) rotate(ctx context.Context, token string, target *uuid.UUID) (Session, error) {
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
		from := tenant
		if target != nil {
			tenant = *target
		}
		role, err := q.GetMembershipRole(ctx, store.GetMembershipRoleParams{UserID: rt.UserID, TenantID: tenant})
		if errors.Is(err, pgx.ErrNoRows) && target != nil {
			// Not one of the caller's tenants: refused, token untouched.
			outcome = ErrNotAMember
			return nil
		}
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
		if err != nil || target == nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.tenant_switched", Subject: subject, TenantID: tenant,
			UserID: rt.UserID, Detail: map[string]any{"from": from.String(), "to": tenant.String(), "role": string(role)}})
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
	Name        *string // nil until the person sets one
	Memberships []store.ListMembershipsForUserRow
}

// Me returns the caller. The memberships span tenants, so it reads them in
// a system transaction filtered by the caller's own user id.
func (s *Service) Me(ctx context.Context, p Principal) (MeView, error) {
	v := MeView{Principal: p}
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		prof, err := q.GetUserProfile(ctx, p.UserID)
		if err != nil {
			return err
		}
		v.Email, v.Name = prof.Email, prof.DisplayName
		v.Memberships, err = q.ListMembershipsForUser(ctx, p.UserID)
		return err
	})
	return v, err
}

// TenantIDs lists every tenant, in a system transaction: the one cross
// tenant read, for platform jobs that do per tenant work (the monthly
// fan out). Ids only; each tenant's work then runs under its own scope.
func TenantIDs(ctx context.Context, d *db.DB) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	err := d.System(ctx, func(tx pgx.Tx) error {
		var err error
		ids, err = TenantIDsTx(ctx, tx)
		return err
	})
	return ids, err
}

// TenantIDsTx is TenantIDs inside a system transaction the caller holds.
func TenantIDsTx(ctx context.Context, tx pgx.Tx) ([]uuid.UUID, error) {
	return store.New(tx).ListTenantIDs(ctx)
}

// Member is a person of a tenant: active, or pending until they accept
// the invitation (D-19).
type Member struct {
	ID          uuid.UUID
	Email       string
	DisplayName *string
	Role        string
	Pending     bool
}

// ListMembers returns the members of the caller's tenant (under RLS), the
// active ones first, then those still pending. A pending person's name is
// never shown: the tenant does not see their account.
func (s *Service) ListMembers(ctx context.Context) ([]Member, error) {
	scope, ok := db.ScopeFrom(ctx)
	if !ok {
		return nil, db.ErrNoScope
	}
	var out []Member
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		active, err := q.ListMembers(ctx, scope.TenantID)
		if err != nil {
			return err
		}
		pending, err := q.ListPendingMembers(ctx, scope.TenantID)
		if err != nil {
			return err
		}
		out = make([]Member, 0, len(active)+len(pending))
		for _, m := range active {
			out = append(out, Member{ID: m.ID, Email: m.Email, DisplayName: m.DisplayName, Role: string(m.Role)})
		}
		for _, m := range pending {
			out = append(out, Member{ID: m.UserID, Email: m.Email, Role: string(m.Role), Pending: true})
		}
		return nil
	})
	return out, err
}

// AddMember invites email to the caller's tenant with role, under the
// grant rules of spec §1 and §2 (CanGrant). Adding is always an invitation
// (D-19): the membership stays pending, giving no access, until the person
// accepts the mailed link (AcceptInvite). Nothing in the person's account
// is written (F25). Adding someone already pending replaces the role and
// mails a fresh invitation. An operator grants only the first owner: with
// the tenant row locked, so two operators can not each invite one.
func (s *Service) AddMember(ctx context.Context, p Principal, email, role string) (Member, error) {
	if !validRole(role) {
		return Member{}, fmt.Errorf("unknown role %q", role)
	}
	if !CanGrant(p.Role, role) {
		return Member{}, ErrForbidden
	}
	firstOwner := p.Role == RoleOperator
	if firstOwner {
		// Early refusal, before any user row is created; the locked
		// check below is the one that counts.
		if err := s.DB.InTenant(ctx, func(tx pgx.Tx) error { return noOwnerYet(ctx, store.New(tx), p.TenantID, false) }); err != nil {
			return Member{}, err
		}
	}
	email, err := normalizeEmail(email)
	if err != nil {
		return Member{}, err
	}
	// users has no tenant and tramo_app cannot insert into it: the user row
	// is ensured in a system transaction, the pending membership under RLS.
	var user uuid.UUID
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		var err error
		user, err = store.New(tx).EnsureUser(ctx, email)
		return err
	})
	if err != nil {
		return Member{}, err
	}
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockTenant(ctx, p.TenantID); err != nil {
			return err
		}
		if _, err := q.GetMembershipRole(ctx, store.GetMembershipRoleParams{UserID: user, TenantID: p.TenantID}); err == nil {
			return ErrAlreadyMember
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if firstOwner {
			if err := firstOwnerFree(ctx, q, p.TenantID, user); err != nil {
				return err
			}
		}
		inviter := p.UserID
		if err := q.UpsertPendingMembership(ctx, store.UpsertPendingMembershipParams{TenantID: p.TenantID, UserID: user,
			Email: email, Role: store.Role(role), InvitedBy: &inviter}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.member_invited", Subject: "users:" + user.String(), Detail: map[string]any{"role": role}})
	})
	if err != nil {
		return Member{}, err
	}
	if err := s.invite(ctx, p, user, role); err != nil {
		return Member{}, err
	}
	return Member{ID: user, Email: email, Role: role, Pending: true}, nil
}

// firstOwnerFree answers ErrForbidden when the tenant has an owner, or a
// pending owner other than user. The tenant row must be locked.
func firstOwnerFree(ctx context.Context, q *store.Queries, tenant, user uuid.UUID) error {
	if err := noOwnerYet(ctx, q, tenant, false); err != nil {
		return err
	}
	n, err := q.CountPendingOwners(ctx, tenant)
	if err != nil {
		return err
	}
	if role, err := q.GetPendingMembershipForUpdate(ctx, store.GetPendingMembershipForUpdateParams{TenantID: tenant, UserID: user}); err == nil && role == store.RoleOwner {
		n--
	} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if n > 0 {
		return ErrForbidden
	}
	return nil
}

// CanRemoveMembers reports whether role may remove people from a tenant.
// Spec §1: only the owner; the operator only creates the first owner.
func CanRemoveMembers(role string) bool { return role == RoleOwner }

// RemoveMember takes user out of the caller's tenant. The last owner can
// not be removed (the tenant row is locked so two owners removing each
// other can not both succeed). The user's refresh tokens for this tenant
// are revoked at once; an access token already issued keeps working until
// it expires, at most AccessTTL (10 minutes).
func (s *Service) RemoveMember(ctx context.Context, p Principal, user uuid.UUID) error {
	if !CanRemoveMembers(p.Role) {
		return ErrForbidden
	}
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockTenant(ctx, p.TenantID); err != nil {
			return err
		}
		role, err := q.DeleteMembership(ctx, store.DeleteMembershipParams{UserID: user, TenantID: p.TenantID})
		if errors.Is(err, pgx.ErrNoRows) {
			// Maybe pending: then the invitation gives nothing any more
			// (AcceptInvite answers not_a_member).
			n, err := q.DeletePendingMembership(ctx, store.DeletePendingMembershipParams{TenantID: p.TenantID, UserID: user})
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrMemberNotFound
			}
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.member_removed", Subject: "users:" + user.String(),
				Detail: map[string]any{"status": "pending"}})
		}
		if err != nil {
			return err
		}
		if role == store.RoleOwner {
			n, err := q.CountOwners(ctx, p.TenantID)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastOwner // rolls the delete back
			}
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.member_removed", Subject: "users:" + user.String(),
			Detail: map[string]any{"role": string(role)}})
	})
	if err != nil {
		return err
	}
	// refresh_tokens is not visible to tramo_app. If this fails the session
	// still ends at the next refresh, which checks the membership.
	return s.DB.System(ctx, func(tx pgx.Tx) error {
		_, err := store.New(tx).RevokeUserTenantSessions(ctx, store.RevokeUserTenantSessionsParams{UserID: user, TenantID: &p.TenantID})
		return err
	})
}

// TenantInfo is the active tenant as stored.
type TenantInfo struct {
	ID   uuid.UUID
	Name string
	CUIT *int64
}

// Tenant reads the tenant of the scope in ctx, under RLS.
func (s *Service) Tenant(ctx context.Context) (TenantInfo, error) {
	scope, ok := db.ScopeFrom(ctx)
	if !ok {
		return TenantInfo{}, db.ErrNoScope
	}
	var t TenantInfo
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		row, err := store.New(tx).GetTenant(ctx, scope.TenantID)
		t = TenantInfo{ID: row.ID, Name: row.Name, CUIT: row.Cuit}
		return err
	})
	return t, err
}

// ConsolePrincipals returns, for the caller, one principal per tenant where
// the caller is operator or accountant, with that membership's role. The
// operator console reads each tenant under its own scope
// (WithPrincipal). Built only from the caller's own memberships.
func (s *Service) ConsolePrincipals(ctx context.Context, p Principal) ([]Principal, error) {
	me, err := s.Me(ctx, p)
	if err != nil {
		return nil, err
	}
	var out []Principal
	for _, m := range me.Memberships {
		if r := string(m.Role); r == RoleOperator || r == RoleAccountant {
			out = append(out, Principal{UserID: p.UserID, TenantID: m.TenantID, Role: r})
		}
	}
	return out, nil
}

// PurgeExpiredRefreshTokens deletes refresh tokens expired more than seven
// days ago. Run by the worker's daily maintenance job.
func PurgeExpiredRefreshTokens(ctx context.Context, d *db.DB) (int64, error) {
	var n int64
	err := d.System(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).DeleteExpiredRefreshTokens(ctx)
		return err
	})
	return n, err
}

// CanGrant reports whether a member with role granter may give role to
// someone over HTTP (spec §1 and §2). The owner grants owner, approver,
// viewer and accountant. The operator grants only owner, and AddMember
// further requires that the tenant has no owner yet. Nobody grants
// operator: the first one comes from `api create-operator`.
func CanGrant(granter, role string) bool {
	switch granter {
	case RoleOwner:
		return role == RoleOwner || role == RoleApprover || role == RoleViewer || role == RoleAccountant
	case RoleOperator:
		return role == RoleOwner
	}
	return false
}

// noOwnerYet answers ErrForbidden when the tenant already has an owner.
// With lock it first takes the tenant row, serializing it with removals and
// other grants.
func noOwnerYet(ctx context.Context, q *store.Queries, tenant uuid.UUID, lock bool) error {
	if lock {
		if _, err := q.LockTenant(ctx, tenant); err != nil {
			return err
		}
	}
	n, err := q.CountOwners(ctx, tenant)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrForbidden
	}
	return nil
}

func validRole(r string) bool {
	switch r {
	case RoleOwner, RoleApprover, RoleViewer, RoleOperator, RoleAccountant:
		return true
	}
	return false
}

// normalizeEmail trims and lowercases an email, so one person is one
// account whatever the case typed (F25), and refuses an email with an
// invisible or bidi character, which would make a second account look
// identical to the real one (F30). Every input that takes an email goes
// through here.
func normalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range s {
		if text.Invisible(r) || text.Space(r) {
			return "", ErrInvalidEmail
		}
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
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
