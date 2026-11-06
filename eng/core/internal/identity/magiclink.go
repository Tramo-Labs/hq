package identity

import (
	"context"
	"errors"
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

// MagicLinkTTL is how long a login link works.
const MagicLinkTTL = 15 * time.Minute

// ErrInvalidMagicLink answers every link that can not be used: unknown,
// used or expired. One answer for all.
var ErrInvalidMagicLink = errors.New("invalid or expired link")

// RequestMagicLink emails a one time login link if the email has an
// account, and does nothing otherwise; the caller answers the same either
// way (202), so it does not reveal which emails exist. Throttled per IP and
// per email before anything else, known emails or not. The token goes only
// into the email: never in a response, a log or the audit log.
func (s *Service) RequestMagicLink(ctx context.Context, email string, tenant *uuid.UUID) error {
	email = strings.TrimSpace(email)
	if err := s.checkMagicLinkRequest(ctx, email); err != nil {
		return err
	}
	token, hash, err := newRefreshToken()
	if err != nil {
		return err
	}
	var sent bool
	err = s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		u, err := q.GetUserForLogin(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := q.InsertMagicLink(ctx, store.InsertMagicLinkParams{UserID: u.ID, TenantID: tenant,
			TokenHash: hash, ExpiresAt: s.now().Add(MagicLinkTTL)}); err != nil {
			return err
		}
		sent = true
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.magic_link_sent", Subject: "users:" + u.ID.String(), UserID: u.ID})
	})
	if err != nil || !sent {
		return err
	}
	if s.Mailer == nil {
		httpx.Logger(ctx).Warn("magic link not delivered: no mail transport configured")
		return nil
	}
	if err := s.Mailer.Send(ctx, magicLinkMessage(s.AppBaseURL, email, token)); err != nil {
		httpx.Logger(ctx).Error("magic link not delivered", "err", err)
	}
	return nil
}

// magicLinkMessage is the login link email.
//
// DRAFT COPY, English, for producto to rewrite (and translate to Spanish
// with voseo) before any real email goes out.
func magicLinkMessage(base, to, token string) mail.Message {
	// The token rides in the URL fragment: browsers do not send it to any
	// server, so it never lands in access logs or Referer headers.
	link := strings.TrimRight(base, "/") + "/ingresar#token=" + token
	return mail.Message{
		To:      to,
		Subject: "Your sign in link",
		Body: "Sign in with this link. It works once and expires in 15 minutes:\n" + link + "\n\n" +
			"If you did not ask for it, ignore this email: nobody can use it but you.",
	}
}

// AcceptMagicLink opens a session with a link: on the tenant asked for when
// it was requested, or the oldest membership. It also forgives this IP's
// login failures for the email. It does not lift the email's magic link
// mode, which keeps applying to password logins from anywhere.
func (s *Service) AcceptMagicLink(ctx context.Context, token string) (Session, error) {
	if err := s.checkMagicAccept(ctx); err != nil {
		return Session{}, err
	}
	var out Session
	var outcome error
	var email string
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		reject := func(subject, reason string) error {
			outcome = ErrInvalidMagicLink
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.magic_link_rejected", Subject: subject,
				Detail: map[string]any{"reason": reason}})
		}
		l, err := q.GetMagicLinkForUpdate(ctx, hashRefresh(token))
		if errors.Is(err, pgx.ErrNoRows) {
			return reject("magic_links:unknown", "unknown")
		}
		if err != nil {
			return err
		}
		subject := "magic_links:" + l.ID.String()
		switch {
		case l.UsedAt != nil:
			return reject(subject, "used")
		case !s.now().Before(l.ExpiresAt):
			return reject(subject, "expired")
		}
		if err := q.MarkMagicLinkUsed(ctx, l.ID); err != nil {
			return err
		}
		if email, err = q.GetUserEmail(ctx, l.UserID); err != nil {
			return err
		}
		p, err := s.pickTenant(ctx, q, l.UserID, l.TenantID)
		if errors.Is(err, ErrNotAMember) {
			outcome = ErrNotAMember
			return audit.Record(ctx, tx, audit.Entry{Action: "identity.login_failed", Subject: "users:" + l.UserID.String(),
				UserID: l.UserID, Detail: map[string]any{"reason": "no membership", "via": "magic_link"}})
		}
		if err != nil {
			return err
		}
		if out, err = s.openSession(ctx, q, p, uuid.New()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.magic_link_login", Subject: subject,
			TenantID: p.TenantID, UserID: p.UserID})
	})
	if err != nil {
		return Session{}, err
	}
	if outcome != nil {
		return Session{}, outcome
	}
	s.loginSucceeded(ctx, email)
	return out, nil
}

// auditMagicLinkMode records that an email was switched to magic link by a
// distributed attack. Only the user id, never the email; nothing for an
// email without an account. A failure to write it is logged.
func (s *Service) auditMagicLinkMode(ctx context.Context, email string) {
	err := s.DB.System(ctx, func(tx pgx.Tx) error {
		u, err := store.New(tx).GetUserForLogin(ctx, email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "identity.magic_link_mode", Subject: "users:" + u.ID.String(), UserID: u.ID,
			Detail: map[string]any{"for_minutes": int(s.Limits.AttackMode / time.Minute)}})
	})
	if err != nil {
		httpx.Logger(ctx).Error("audit magic link mode", "err", err)
	}
}

// PurgeExpiredMagicLinks deletes links expired more than seven days ago.
func PurgeExpiredMagicLinks(ctx context.Context, d *db.DB) (int64, error) {
	var n int64
	err := d.System(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).DeleteExpiredMagicLinks(ctx)
		return err
	})
	return n, err
}
