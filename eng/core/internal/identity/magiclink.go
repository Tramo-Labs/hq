package identity

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

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

// ErrNoQueue means the service was built without a job queue (Enqueue).
var ErrNoQueue = errors.New("identity: no job queue configured")

// MagicLinkArgs is the job that sends one magic link. It carries only the
// id of the pending request (magic_link_requests): River stores job
// arguments in clear, and personal data (the email, the IP) never goes
// there (worker README).
type MagicLinkArgs struct {
	Request uuid.UUID `json:"request"`
}

// Kind is the stable job name stored in the queue.
func (MagicLinkArgs) Kind() string { return "identity.magic_link" }

// InsertOpts: unique by the request, so a retried insert of the same
// request is not sent twice. The per email limit (three per 15 minutes) is
// enforced in the request, before anything is stored.
func (MagicLinkArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}, MaxAttempts: 3}
}

// RequestMagicLink asks for a one time login link. It checks the
// throttles (per IP and per email, known emails or not), stores the
// request and enqueues a job with the request's id; it never looks the
// email up. So the request does the same work, and takes the same time,
// whether or not the email has an account, and the caller answers 202
// either way (D-17). The job sends the link if there is an account and
// does nothing otherwise. A request whose enqueue failed is purged daily.
func (s *Service) RequestMagicLink(ctx context.Context, email string, tenant *uuid.UUID) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := s.checkMagicLinkRequest(ctx, email); err != nil {
		return err
	}
	if s.Enqueue == nil {
		return ErrNoQueue
	}
	in := store.InsertMagicLinkRequestParams{Email: email, TenantID: tenant}
	if m, ok := httpx.MetaFrom(ctx); ok {
		in.RequestID = &m.RequestID
		if m.IP.IsValid() {
			in.Ip = m.IP.String()
		}
	}
	var id uuid.UUID
	if err := s.DB.System(ctx, func(tx pgx.Tx) error {
		var err error
		id, err = store.New(tx).InsertMagicLinkRequest(ctx, in)
		return err
	}); err != nil {
		return err
	}
	return s.Enqueue(ctx, MagicLinkArgs{Request: id})
}

// MagicLinkWorker sends the links asked for with RequestMagicLink.
type MagicLinkWorker struct {
	river.WorkerDefaults[MagicLinkArgs]
	DB         *db.DB
	Mailer     mail.Sender
	AppBaseURL string
}

// Work takes the pending request (deleting it), and in that same
// transaction looks the email up and, if it has an account, stores the
// link; then mails it. A second run finds no request and does nothing.
//
// A link that could only answer 403 is not sent (F36): when the person
// asked for a company where their membership is pending, or has no active
// membership but pending ones, the invitation goes again instead, at most
// maxResent of them. A new token that revokes nothing: anyone can ask with
// the email, and must not kill the link the person already has. The
// request's public answer was the same 202 either way.
func (w *MagicLinkWorker) Work(ctx context.Context, job *river.Job[MagicLinkArgs]) error {
	token, hash, err := newRefreshToken()
	if err != nil {
		return err
	}
	var email string
	var sent bool
	var resent []issuedInvite
	var user uuid.UUID
	err = w.DB.System(ctx, func(tx pgx.Tx) error {
		q := store.New(tx)
		req, err := q.TakeMagicLinkRequest(ctx, job.Args.Request)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // already sent, or purged
		}
		if err != nil {
			return err
		}
		email = req.Email
		// The audit row of the job carries the request's id and IP.
		m := httpx.Meta{RequestID: req.RequestID}
		if ip, err := netip.ParseAddr(req.Ip); err == nil {
			m.IP = ip
		}
		actx := httpx.WithMeta(ctx, m)
		u, err := q.GetUserForLogin(actx, req.Email)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// To the stored address, not the casing typed (F35).
		if email, err = q.GetUserEmail(actx, u.ID); err != nil {
			return err
		}
		user = u.ID
		targets, err := pendingTargets(actx, q, u.ID, req.TenantID)
		if err != nil {
			return err
		}
		if len(targets) > 0 {
			for _, p := range targets {
				iv, err := issueInvite(actx, q, p.TenantID, u.ID, p.InvitedBy, string(p.Role), time.Now(), false)
				if err != nil {
					return err
				}
				resent = append(resent, iv)
				if err := audit.Record(actx, tx, audit.Entry{Action: "identity.invite_resent", Subject: "users:" + u.ID.String(),
					TenantID: p.TenantID, UserID: u.ID, Detail: map[string]any{"via": "magic_link"}}); err != nil {
					return err
				}
			}
			return nil
		}
		if err := q.InsertMagicLink(actx, store.InsertMagicLinkParams{UserID: u.ID, TenantID: req.TenantID,
			TokenHash: hash, ExpiresAt: time.Now().Add(MagicLinkTTL)}); err != nil {
			return err
		}
		sent = true
		return audit.Record(actx, tx, audit.Entry{Action: "identity.magic_link_sent", Subject: "users:" + u.ID.String(), UserID: u.ID})
	})
	if err != nil {
		return err
	}
	for _, iv := range resent {
		sendInvite(ctx, w.Mailer, w.AppBaseURL, user, iv)
	}
	if !sent {
		return nil
	}
	if w.Mailer == nil {
		httpx.Logger(ctx).Warn("magic link not delivered: no mail transport configured")
		return nil
	}
	if err := w.Mailer.Send(ctx, magicLinkMessage(w.AppBaseURL, email, token)); err != nil {
		httpx.Logger(ctx).Error("magic link not delivered", "err", err)
	}
	return nil
}

// maxResent bounds the invitations one link request sends again. With the
// request capped at 3 per email every 15 minutes, a person gets at most 15
// resent invitations in that time, all in their own inbox.
const maxResent = 5

// pendingTargets lists the pending invitations a link request should get
// instead of a link: the one to the company asked for, or, with no company
// asked and no active membership, the person's pending ones (oldest first,
// at most maxResent). None means a link is the right answer.
func pendingTargets(ctx context.Context, q *store.Queries, user uuid.UUID, tenant *uuid.UUID) ([]store.ListPendingForUserRow, error) {
	pending, err := q.ListPendingForUser(ctx, user)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	if tenant != nil {
		for _, p := range pending {
			if p.TenantID == *tenant {
				return []store.ListPendingForUserRow{p}, nil
			}
		}
		return nil, nil
	}
	active, err := q.ListMembershipsForUser(ctx, user)
	if err != nil || len(active) > 0 {
		return nil, err
	}
	if len(pending) > maxResent {
		pending = pending[:maxResent]
	}
	return pending, nil
}

// PurgeStaleMagicLinkRequests deletes requests older than a day: left only
// when the enqueue after storing one failed.
func PurgeStaleMagicLinkRequests(ctx context.Context, d *db.DB) (int64, error) {
	var n int64
	err := d.System(ctx, func(tx pgx.Tx) error {
		var err error
		n, err = store.New(tx).DeleteStaleMagicLinkRequests(ctx)
		return err
	})
	return n, err
}

// magicLinkMessage is the login link email, in Spanish with voseo (F31).
// Copy written by backend; producto owns it from here.
func magicLinkMessage(base, to, token string) mail.Message {
	// The token rides in the URL fragment: browsers do not send it to any
	// server, so it never lands in access logs or Referer headers.
	link := strings.TrimRight(base, "/") + "/ingresar#token=" + token
	return mail.Message{
		To:      to,
		Subject: "Tu enlace para entrar",
		Body: "Entrá con este enlace. Sirve una sola vez y vence en 15 minutos:\n" + link + "\n\n" +
			"Si no lo pediste, ignorá este mail: nadie más que vos puede usarlo.",
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
