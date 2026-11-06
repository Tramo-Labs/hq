package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Password length bounds, the same as components.schemas.Password.
const (
	MinPassword = 12
	MaxPassword = 128
)

// ErrAlreadyOtherRole: the person is already a member of the tenant with
// another role; create-operator does not change roles.
var ErrAlreadyOtherRole = errors.New("already a member of that tenant with another role")

// OperatorResult says what CreateOperator did.
type OperatorResult struct {
	UserID      uuid.UUID
	UserCreated bool // a new user row
	PasswordSet bool // the password was set now (it had none)
	Added       bool // false: already operator, nothing changed
}

// CreateOperator bootstraps an operator of tenant: the only way to grant
// the operator role (no HTTP operation does). It is idempotent: an existing
// operator membership is left as it is. A user who already has a password
// keeps it; one without gets password. Every change is audited as
// identity.operator_created with request id "cli-<uuid>", no user and no
// IP. Runs in a system transaction.
func CreateOperator(ctx context.Context, d *db.DB, argon Argon2Params, email string, tenant uuid.UUID, password string) (OperatorResult, error) {
	var out OperatorResult
	email, err := normalizeEmail(email)
	if err != nil {
		return out, err
	}
	if n := len(password); n < MinPassword || n > MaxPassword {
		return out, fmt.Errorf("password must have %d to %d characters", MinPassword, MaxPassword)
	}
	hash, err := HashPassword(argon, password)
	if err != nil {
		return out, err
	}
	ctx = httpx.WithMeta(ctx, httpx.Meta{RequestID: "cli-" + uuid.NewString()})
	err = d.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.LockTenant(ctx, tenant); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("tenant %s does not exist", tenant)
			}
			return err
		}
		u, err := q.GetUserForLogin(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if out.UserID, err = q.CreateUser(ctx, store.CreateUserParams{Email: email, PasswordHash: &hash}); err != nil {
				return err
			}
			out.UserCreated, out.PasswordSet = true, true
		case err != nil:
			return err
		default:
			out.UserID = u.ID
			if u.PasswordHash == nil {
				n, err := q.SetPasswordIfUnset(ctx, store.SetPasswordIfUnsetParams{ID: u.ID, PasswordHash: &hash})
				if err != nil {
					return err
				}
				out.PasswordSet = n == 1
			}
		}
		role, err := q.GetMembershipRole(ctx, store.GetMembershipRoleParams{UserID: out.UserID, TenantID: tenant})
		switch {
		case err == nil && role == store.RoleOperator:
			if out.PasswordSet {
				return audit.Record(ctx, tx, audit.Entry{Action: "identity.password_set", Subject: "users:" + out.UserID.String(),
					TenantID: tenant, Detail: map[string]any{"via": "cli"}})
			}
			return nil
		case err == nil:
			return ErrAlreadyOtherRole
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := q.CreateMembership(ctx, store.CreateMembershipParams{UserID: out.UserID, TenantID: tenant, Role: store.RoleOperator}); err != nil {
			return err
		}
		out.Added = true
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.operator_created", Subject: "users:" + out.UserID.String(),
			TenantID: tenant, Detail: map[string]any{"via": "cli", "user_created": out.UserCreated}})
	})
	return out, err
}
