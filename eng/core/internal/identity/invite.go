package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity/store"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
)

// InviteTTL is how long an invitation link works.
const InviteTTL = 72 * time.Hour

// ErrInvalidInvite answers every invitation that can not be used: unknown,
// used, revoked or expired. One answer for all, so the endpoint tells
// nothing about which.
var ErrInvalidInvite = errors.New("invalid or expired invitation")

// invite mails the invitation to a pending member, with or without an
// account (D-19). The token goes only into the email: never in a response,
// a log or the audit log. A delivery failure is logged; the pending
// membership stays, and adding the person again sends a new invitation.
func (s *Service) invite(ctx context.Context, p Principal, user uuid.UUID, role string) error {
	var iv issuedInvite
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		var err error
		inviter := p.UserID
		if iv, err = issueInvite(ctx, store.New(tx), p.TenantID, user, &inviter, role, s.now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_created", Subject: "users:" + user.String(),
			TenantID: p.TenantID, UserID: p.UserID, Detail: map[string]any{"role": role}})
	})
	if err != nil {
		return err
	}
	sendInvite(ctx, s.Mailer, s.AppBaseURL, user, iv)
	return nil
}

// issuedInvite is an invitation stored and ready to mail.
type issuedInvite struct {
	to, tenantName, role, token string
	hasPassword                 bool
}

// issueInvite stores a new invitation of user to tenant and revokes the
// person's earlier unused ones to that tenant only: one from another
// tenant stays valid. It goes to the address stored for the account
// (F35). Used when a person is added and when a pending person asks for a
// sign in link (F36).
func issueInvite(ctx context.Context, q *store.Queries, tenant, user uuid.UUID, createdBy *uuid.UUID, role string, now time.Time) (issuedInvite, error) {
	token, hash, err := newRefreshToken()
	if err != nil {
		return issuedInvite{}, err
	}
	iv := issuedInvite{role: role, token: token}
	if iv.hasPassword, err = q.GetUserPasswordSet(ctx, user); err != nil {
		return iv, err
	}
	if iv.to, err = q.GetUserEmail(ctx, user); err != nil {
		return iv, err
	}
	t, err := q.GetTenant(ctx, tenant)
	if err != nil {
		return iv, err
	}
	iv.tenantName = t.Name
	if _, err := q.RevokeOpenTenantInvites(ctx, store.RevokeOpenTenantInvitesParams{UserID: user, TenantID: tenant}); err != nil {
		return iv, err
	}
	err = q.InsertInvite(ctx, store.InsertInviteParams{TenantID: tenant, UserID: user, CreatedBy: createdBy,
		TokenHash: hash, ExpiresAt: now.Add(InviteTTL)})
	return iv, err
}

// sendInvite mails iv. A failure, or no transport, is logged: the
// invitation stays stored and a new one can be sent.
func sendInvite(ctx context.Context, m mail.Sender, base string, user uuid.UUID, iv issuedInvite) {
	if m == nil {
		httpx.Logger(ctx).Warn("invitation not delivered: no mail transport configured", "user", user)
		return
	}
	if err := m.Send(ctx, inviteMessage(base, iv.to, iv.tenantName, iv.role, iv.token, iv.hasPassword)); err != nil {
		httpx.Logger(ctx).Error("invitation not delivered", "user", user, "err", err)
	}
}

// inviteMessage is the invitation email, in Spanish with voseo (F31). A
// person with an account only accepts; one without also sets a password.
// Copy written by backend; producto owns it from here.
func inviteMessage(base, to, tenantName, role, token string, hasPassword bool) mail.Message {
	// The token rides in the URL fragment: browsers do not send it to any
	// server, so it never lands in access logs or Referer headers.
	link := strings.TrimRight(base, "/") + "/invitacion#token=" + token
	action := "Elegí tu contraseña y entrá desde este enlace"
	if hasPassword {
		action = "Aceptá la invitación con tu cuenta desde este enlace"
	}
	return mail.Message{
		To:      to,
		Subject: "Tu invitación",
		Body: fmt.Sprintf("Te sumaron a %s con el rol %s. Hasta que aceptes no tenés acceso.\n\n"+
			"%s. Sirve una sola vez y vence en 72 horas:\n%s\n\n"+
			"Si no esperabas este mail, ignoralo.", tenantName, roleLabel(role), action, link),
	}
}

// roleLabel is a role's name in the product's Spanish, the same the portal
// shows (eng/web/src/content/app.ts).
func roleLabel(role string) string {
	switch role {
	case RoleOwner:
		return "titular"
	case RoleApprover:
		return "aprobador"
	case RoleViewer:
		return "lectura"
	case RoleAccountant:
		return "contador"
	case RoleOperator:
		return "operador"
	}
	return role
}

// AcceptInvite turns the pending membership into a membership and opens a
// session on that tenant. A person without a password sets one here
// (ErrPasswordRequired without it, leaving the invitation usable); one
// with an account does not, and their password is never changed. An
// invitation whose pending membership was removed answers ErrNotAMember
// and writes nothing (F29). Any token that can not be used answers
// ErrInvalidInvite. Every refusal is audited.
//
// Asking a new person for a password (ErrPasswordRequired) is a step, not a
// refusal: the portal tries without one first. It is neither audited as a
// rejection nor counted toward the per IP cap; it is only reachable with a
// valid token, so it tells nothing to someone guessing. Every other
// outcome, success included, counts.
func (s *Service) AcceptInvite(ctx context.Context, token, password string, name *string) (Session, error) {
	giveBack, err := s.checkInvite(ctx)
	if err != nil {
		return Session{}, err
	}
	sess, err := s.acceptInvite(ctx, token, password, name)
	if errors.Is(err, ErrPasswordRequired) {
		giveBack()
	}
	return sess, err
}

func (s *Service) acceptInvite(ctx context.Context, token, password string, name *string) (Session, error) {
	if n := len(password); n > 0 && (n < MinPassword || n > MaxPassword) {
		return Session{}, ErrInvalidInvite
	}
	displayName, err := optionalName(name)
	if err != nil {
		return Session{}, err
	}
	var out Session
	var outcome error
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		reject := func(subject string, tenant uuid.UUID, reason string, answer error) error {
			outcome = answer
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_rejected", Subject: subject, TenantID: tenant,
				Detail: map[string]any{"reason": reason}})
		}
		inv, err := q.GetInviteForUpdate(ctx, hashRefresh(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("invites:unknown", uuid.Nil, "unknown", ErrInvalidInvite)
		}
		if err != nil {
			return err
		}
		subject := "invites:" + inv.ID.String()
		switch {
		case inv.UsedAt != nil:
			return reject(subject, inv.TenantID, "used", ErrInvalidInvite)
		case inv.RevokedAt != nil:
			return reject(subject, inv.TenantID, "revoked", ErrInvalidInvite)
		case !s.now().Before(inv.ExpiresAt):
			return reject(subject, inv.TenantID, "expired", ErrInvalidInvite)
		}
		// Everything is checked before anything is written: a refusal
		// leaves no password and no membership behind (F29).
		role, err := q.GetPendingMembershipForUpdate(ctx, store.GetPendingMembershipForUpdateParams{TenantID: inv.TenantID, UserID: inv.UserID})
		pending := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !pending {
			// An invitation mailed before D-19 came with the membership
			// already active; any other is for a removed person.
			if _, err := q.GetMembershipRole(ctx, store.GetMembershipRoleParams{UserID: inv.UserID, TenantID: inv.TenantID}); errors.Is(err, pgx.ErrNoRows) {
				return reject(subject, inv.TenantID, "no membership", ErrNotAMember)
			} else if err != nil {
				return err
			}
		}
		hasPassword, err := q.GetUserPasswordSet(ctx, inv.UserID)
		if err != nil {
			return err
		}
		if !hasPassword && password == "" {
			outcome = ErrPasswordRequired // a step, not a rejection
			return nil
		}
		if !pending && hasPassword {
			return reject(subject, inv.TenantID, "password already set", ErrInvalidInvite)
		}
		if err := q.MarkInviteUsed(ctx, inv.ID); err != nil {
			return err
		}
		if !hasPassword {
			hash, err := HashPassword(s.Argon, password)
			if err != nil {
				return err
			}
			if _, err := q.SetPasswordIfUnset(ctx, store.SetPasswordIfUnsetParams{ID: inv.UserID, PasswordHash: &hash}); err != nil {
				return err
			}
			// The name given here, by the person, if they have none yet.
			if displayName != nil {
				if err := q.SetUserNameIfUnset(ctx, store.SetUserNameIfUnsetParams{ID: inv.UserID, DisplayName: displayName}); err != nil {
					return err
				}
			}
		}
		if pending {
			if _, err := q.DeletePendingMembership(ctx, store.DeletePendingMembershipParams{TenantID: inv.TenantID, UserID: inv.UserID}); err != nil {
				return err
			}
			if err := q.CreateMembership(ctx, store.CreateMembershipParams{UserID: inv.UserID, TenantID: inv.TenantID, Role: role}); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_accepted", Subject: subject,
			TenantID: inv.TenantID, UserID: inv.UserID}); err != nil {
			return err
		}
		p, err := s.pickTenant(ctx, q, inv.UserID, &inv.TenantID)
		if err != nil {
			return err
		}
		out, err = s.openSession(ctx, q, p, uuid.New())
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

// PurgeExpiredInvites deletes invitations expired more than seven days ago.
func PurgeExpiredInvites(ctx context.Context, d *db.DB) (int64, error) {
	var n int64
	err := d.System(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).DeleteExpiredInvites(ctx)
		return err
	})
	return n, err
}
