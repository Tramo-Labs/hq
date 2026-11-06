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
// used, revoked, expired, or for a person who already has a password. One
// answer for all, so the endpoint tells nothing about which.
var ErrInvalidInvite = errors.New("invalid or expired invitation")

// inviteIfNoPassword sends an invitation to a member who has no password
// yet. The token goes only into the email: never in a response, a log or
// the audit log. A new invitation revokes the person's earlier unused
// ones. A delivery failure is logged; the membership stays.
func (s *Service) inviteIfNoPassword(ctx context.Context, p Principal, user uuid.UUID, email, role string) error {
	token, hash, err := newRefreshToken()
	if err != nil {
		return err
	}
	var invited bool
	var tenantName string
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		has, err := q.GetUserPasswordSet(ctx, user)
		if err != nil || has {
			return err
		}
		t, err := q.GetTenant(ctx, p.TenantID)
		if err != nil {
			return err
		}
		tenantName = t.Name
		if _, err := q.RevokeOpenInvites(ctx, user); err != nil {
			return err
		}
		inviter := p.UserID
		if err := q.InsertInvite(ctx, store.InsertInviteParams{TenantID: p.TenantID, UserID: user, CreatedBy: &inviter,
			TokenHash: hash, ExpiresAt: s.now().Add(InviteTTL)}); err != nil {
			return err
		}
		invited = true
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_created", Subject: "users:" + user.String(),
			TenantID: p.TenantID, UserID: p.UserID, Detail: map[string]any{"role": role}})
	})
	if err != nil || !invited {
		return err
	}
	if s.Mailer == nil {
		httpx.Logger(ctx).Warn("invitation not delivered: no mail transport configured", "user", user)
		return nil
	}
	if err := s.Mailer.Send(ctx, inviteMessage(s.AppBaseURL, email, tenantName, role, token)); err != nil {
		httpx.Logger(ctx).Error("invitation not delivered", "user", user, "err", err)
	}
	return nil
}

// inviteMessage is the invitation email.
//
// DRAFT COPY, English, for producto to rewrite (and translate to Spanish
// with voseo) before any real email goes out.
func inviteMessage(base, to, tenantName, role, token string) mail.Message {
	// The token rides in the URL fragment: browsers do not send it to any
	// server, so it never lands in access logs or Referer headers.
	link := strings.TrimRight(base, "/") + "/invitacion#token=" + token
	return mail.Message{
		To:      to,
		Subject: "Your invitation",
		Body: fmt.Sprintf("You were added to %s as %s.\n\n"+
			"Set your password here. The link works once and expires in 72 hours:\n%s\n\n"+
			"If you did not expect this email, ignore it.", tenantName, role, link),
	}
}

// AcceptInvite sets the password of the invited person and opens a session
// on the inviting tenant (or, if that membership was removed meanwhile, on
// their oldest one). Any token that can not be used answers
// ErrInvalidInvite and the attempt is audited.
func (s *Service) AcceptInvite(ctx context.Context, token, password string) (Session, error) {
	if err := s.checkInvite(ctx); err != nil {
		return Session{}, err
	}
	if n := len(password); n < MinPassword || n > MaxPassword {
		return Session{}, ErrInvalidInvite
	}
	hash, err := HashPassword(s.Argon, password)
	if err != nil {
		return Session{}, err
	}
	var out Session
	var outcome error
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		reject := func(subject string, tenant uuid.UUID, reason string) error {
			outcome = ErrInvalidInvite
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_rejected", Subject: subject, TenantID: tenant,
				Detail: map[string]any{"reason": reason}})
		}
		inv, err := q.GetInviteForUpdate(ctx, hashRefresh(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("invites:unknown", uuid.Nil, "unknown")
		}
		if err != nil {
			return err
		}
		subject := "invites:" + inv.ID.String()
		switch {
		case inv.UsedAt != nil:
			return reject(subject, inv.TenantID, "used")
		case inv.RevokedAt != nil:
			return reject(subject, inv.TenantID, "revoked")
		case !s.now().Before(inv.ExpiresAt):
			return reject(subject, inv.TenantID, "expired")
		}
		if err := q.MarkInviteUsed(ctx, inv.ID); err != nil {
			return err
		}
		n, err := q.SetPasswordIfUnset(ctx, store.SetPasswordIfUnsetParams{ID: inv.UserID, PasswordHash: &hash})
		if err != nil {
			return err
		}
		if n == 0 {
			return reject(subject, inv.TenantID, "password already set")
		}
		if err := audit.Record(ctx, tx, audit.Entry{Action: "identity.invite_accepted", Subject: subject,
			TenantID: inv.TenantID, UserID: inv.UserID}); err != nil {
			return err
		}
		p, err := s.pickTenant(ctx, q, inv.UserID, &inv.TenantID)
		if errors.Is(err, ErrNotAMember) {
			p, err = s.pickTenant(ctx, q, inv.UserID, nil)
		}
		if errors.Is(err, ErrNotAMember) {
			outcome = ErrNotAMember // the password is set; no tenant to enter
			return nil
		}
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
