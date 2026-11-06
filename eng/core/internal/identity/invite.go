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
// a log or the audit log. It revokes the person's earlier unused
// invitations to this tenant only: one from another tenant stays valid. A
// delivery failure is logged; the pending membership stays, and adding the
// person again sends a new invitation.
func (s *Service) invite(ctx context.Context, p Principal, user uuid.UUID, email, role string) error {
	token, hash, err := newRefreshToken()
	if err != nil {
		return err
	}
	var hasPassword bool
	var tenantName string
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if hasPassword, err = q.GetUserPasswordSet(ctx, user); err != nil {
			return err
		}
		t, err := q.GetTenant(ctx, p.TenantID)
		if err != nil {
			return err
		}
		tenantName = t.Name
		if _, err := q.RevokeOpenTenantInvites(ctx, store.RevokeOpenTenantInvitesParams{UserID: user, TenantID: p.TenantID}); err != nil {
			return err
		}
		inviter := p.UserID
		if err := q.InsertInvite(ctx, store.InsertInviteParams{TenantID: p.TenantID, UserID: user, CreatedBy: &inviter,
			TokenHash: hash, ExpiresAt: s.now().Add(InviteTTL)}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_created", Subject: "users:" + user.String(),
			TenantID: p.TenantID, UserID: p.UserID, Detail: map[string]any{"role": role}})
	})
	if err != nil {
		return err
	}
	if s.Mailer == nil {
		httpx.Logger(ctx).Warn("invitation not delivered: no mail transport configured", "user", user)
		return nil
	}
	if err := s.Mailer.Send(ctx, inviteMessage(s.AppBaseURL, email, tenantName, role, token, hasPassword)); err != nil {
		httpx.Logger(ctx).Error("invitation not delivered", "user", user, "err", err)
	}
	return nil
}

// inviteMessage is the invitation email. A person with an account only
// accepts; one without also sets a password.
//
// DRAFT COPY, English, for producto to rewrite (and translate to Spanish
// with voseo) before any real email goes out.
func inviteMessage(base, to, tenantName, role, token string, hasPassword bool) mail.Message {
	// The token rides in the URL fragment: browsers do not send it to any
	// server, so it never lands in access logs or Referer headers.
	link := strings.TrimRight(base, "/") + "/invitacion#token=" + token
	action := "Set your password and join here"
	if hasPassword {
		action = "Accept here, with your account"
	}
	return mail.Message{
		To:      to,
		Subject: "Your invitation",
		Body: fmt.Sprintf("You were invited to %s as %s. You are not in until you accept.\n\n"+
			"%s. The link works once and expires in 72 hours:\n%s\n\n"+
			"If you did not expect this email, ignore it.", tenantName, role, action, link),
	}
}

// AcceptInvite turns the pending membership into a membership and opens a
// session on that tenant. A person without a password sets one here
// (ErrPasswordRequired without it, leaving the invitation usable); one
// with an account does not, and their password is never changed. An
// invitation whose pending membership was removed answers ErrNotAMember
// and writes nothing (F29). Any token that can not be used answers
// ErrInvalidInvite. Every refusal is audited.
func (s *Service) AcceptInvite(ctx context.Context, token, password string, name *string) (Session, error) {
	if err := s.checkInvite(ctx); err != nil {
		return Session{}, err
	}
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
			return reject(subject, inv.TenantID, "password required", ErrPasswordRequired)
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
