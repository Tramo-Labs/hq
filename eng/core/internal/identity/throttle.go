package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// Limits are the throttles on the public identity endpoints. They are
// checked before any password hashing, so a flood costs no argon2 time.
//
// D-17: login is limited per IP and per email and IP pair, never per email
// alone. Many failures on one email from many IPs do not lock it: they
// switch it to magic link, so the owner always has a way in.
type Limits struct {
	// Every login request from one IP (spraying many emails).
	LoginPerIP ratelimit.Rule
	// Failed logins for one email from one IP (guessing one password).
	FailPerEmailIP ratelimit.Rule
	// Distributed guessing: AttackFails.Limit failures on one email within
	// AttackFails.Window, from at least AttackIPs distinct IPs, switch the
	// email to magic link for AttackMode.
	AttackFails ratelimit.Rule
	AttackIPs   int
	AttackMode  time.Duration
	// Registrations from one IP.
	RegisterPerIP ratelimit.Rule
	// Invitation acceptances from one IP (tokens are 256 bit; this only
	// bounds the argon2 work an anonymous caller can cause).
	InvitePerIP ratelimit.Rule
	// Magic link requests from one IP, and links sent to one email (from
	// any IP, known or not: a limit on sending, against mail bombing).
	MagicLinkPerIP    ratelimit.Rule
	MagicLinkPerEmail ratelimit.Rule
	// Magic link acceptances from one IP.
	MagicAcceptPerIP ratelimit.Rule
}

// DefaultLimits are deliberately loose for people and tight for scripts.
var DefaultLimits = Limits{
	LoginPerIP:        ratelimit.Rule{Limit: 30, Window: time.Minute},
	FailPerEmailIP:    ratelimit.Rule{Limit: 5, Window: 15 * time.Minute},
	AttackFails:       ratelimit.Rule{Limit: 20, Window: 15 * time.Minute},
	AttackIPs:         3,
	AttackMode:        time.Hour,
	RegisterPerIP:     ratelimit.Rule{Limit: 10, Window: time.Hour},
	InvitePerIP:       ratelimit.Rule{Limit: 10, Window: 15 * time.Minute},
	MagicLinkPerIP:    ratelimit.Rule{Limit: 10, Window: 15 * time.Minute},
	MagicLinkPerEmail: ratelimit.Rule{Limit: 3, Window: 15 * time.Minute},
	MagicAcceptPerIP:  ratelimit.Rule{Limit: 10, Window: 15 * time.Minute},
}

// ErrRateLimited is returned when a throttle trips.
var ErrRateLimited = errors.New("too many attempts")

// ErrMagicLinkRequired answers every password login for an email under a
// distributed attack, right or wrong password alike.
var ErrMagicLinkRequired = errors.New("sign in with a link sent to your email")

// RateLimitedError carries how long the caller should wait, and whether a
// magic link still lets the person in (the email and IP pair throttle).
type RateLimitedError struct {
	RetryAfter time.Duration
	MagicLink  bool
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("%v, retry in %s", ErrRateLimited, e.RetryAfter.Round(time.Second))
}

// Is makes errors.Is(err, ErrRateLimited) true.
func (e *RateLimitedError) Is(target error) bool { return target == ErrRateLimited }

func limited(d time.Duration) error { return &RateLimitedError{RetryAfter: d} }

// clientIP is the request IP from httpx.RequestMeta ("unknown" outside HTTP).
func clientIP(ctx context.Context) string {
	if m, ok := httpx.MetaFrom(ctx); ok && m.IP.IsValid() {
		return m.IP.String()
	}
	return "unknown"
}

func emailKey(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func pairKey(email, ip string) string { return "login:fail:" + emailKey(email) + "|" + ip }

func (s *Service) attacks() *attackWatch {
	s.watchOnce.Do(func() { s.watch = newAttackWatch(s.Limits) })
	return s.watch
}

// checkLogin counts the request against the IP and refuses when a login
// throttle is exhausted, or when the email is in magic link mode. Nothing
// here looks at the password.
func (s *Service) checkLogin(ctx context.Context, email string) error {
	ip := clientIP(ctx)
	if ok, retry := s.Limiter.Allow("login:ip:"+ip, s.Limits.LoginPerIP); !ok {
		return limited(retry)
	}
	if ex, retry := s.Limiter.Exceeded(pairKey(email, ip), s.Limits.FailPerEmailIP); ex {
		return &RateLimitedError{RetryAfter: retry, MagicLink: true}
	}
	if s.attacks().inMode(emailKey(email), s.now()) {
		return ErrMagicLinkRequired
	}
	return nil
}

// loginFailed counts a failure for the pair and for the email's attack
// watch. It reports whether this failure switched the email to magic link.
func (s *Service) loginFailed(ctx context.Context, email string) bool {
	ip := clientIP(ctx)
	s.Limiter.Add(pairKey(email, ip), s.Limits.FailPerEmailIP)
	return s.attacks().fail(emailKey(email), ip, s.now())
}

// loginSucceeded forgives this IP's failures for the email. Failures from
// other IPs keep counting in the attack watch until their window ends.
func (s *Service) loginSucceeded(ctx context.Context, email string) {
	s.Limiter.Reset(pairKey(email, clientIP(ctx)))
}

func (s *Service) checkInvite(ctx context.Context) error {
	if ok, retry := s.Limiter.Allow("invite:ip:"+clientIP(ctx), s.Limits.InvitePerIP); !ok {
		return limited(retry)
	}
	return nil
}

func (s *Service) checkRegister(ctx context.Context) error {
	if ok, retry := s.Limiter.Allow("register:ip:"+clientIP(ctx), s.Limits.RegisterPerIP); !ok {
		return limited(retry)
	}
	return nil
}

func (s *Service) checkMagicLinkRequest(ctx context.Context, email string) error {
	if ok, retry := s.Limiter.Allow("magic:ip:"+clientIP(ctx), s.Limits.MagicLinkPerIP); !ok {
		return limited(retry)
	}
	if ok, retry := s.Limiter.Allow("magic:email:"+emailKey(email), s.Limits.MagicLinkPerEmail); !ok {
		return limited(retry)
	}
	return nil
}

func (s *Service) checkMagicAccept(ctx context.Context) error {
	if ok, retry := s.Limiter.Allow("magic:accept:"+clientIP(ctx), s.Limits.MagicAcceptPerIP); !ok {
		return limited(retry)
	}
	return nil
}

// Bounds of the attack watch: an email needs only AttackIPs distinct
// addresses to be judged, so a few more are plenty; the email cap matches
// the order of the general limiter.
const (
	maxTrackedIPs    = 8
	maxTrackedEmails = 100_000
)

// attackWatch counts failures per email in fixed windows, with the set of
// distinct IPs they came from, and keeps the magic link mode of each email.
// In memory and per process, like ratelimit.
type attackWatch struct {
	mu        sync.Mutex
	limits    Limits
	emails    map[string]*attack
	maxEmails int
}

type attack struct {
	start     time.Time
	fails     int
	ips       map[string]struct{}
	modeUntil time.Time
}

func newAttackWatch(l Limits) *attackWatch {
	return &attackWatch{limits: l, emails: map[string]*attack{}, maxEmails: maxTrackedEmails}
}

// fail records a failure on email from ip and reports whether it switched
// the email to magic link.
func (w *attackWatch) fail(email, ip string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	a := w.emails[email]
	if a == nil {
		if len(w.emails) >= w.maxEmails {
			w.evict(now)
		}
		if len(w.emails) >= w.maxEmails {
			return false // full of emails in the mode: count nothing new
		}
		a = &attack{start: now, ips: map[string]struct{}{}}
		w.emails[email] = a
	}
	if !now.Before(a.start.Add(w.limits.AttackFails.Window)) {
		a.start, a.fails, a.ips = now, 0, map[string]struct{}{}
	}
	a.fails++
	if len(a.ips) < maxTrackedIPs {
		a.ips[ip] = struct{}{}
	}
	if a.fails >= w.limits.AttackFails.Limit && len(a.ips) >= w.limits.AttackIPs {
		started := !now.Before(a.modeUntil)
		a.modeUntil = now.Add(w.limits.AttackMode)
		return started
	}
	return false
}

func (w *attackWatch) inMode(email string, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	a := w.emails[email]
	return a != nil && now.Before(a.modeUntil)
}

// evict drops emails whose window ended and that are not in the mode; if
// none, the one with the oldest window not in the mode. An email in the
// mode is never dropped: flooding the watch cannot lift it.
func (w *attackWatch) evict(now time.Time) {
	var oldest string
	for e, a := range w.emails {
		if !now.Before(a.modeUntil) && !now.Before(a.start.Add(w.limits.AttackFails.Window)) {
			delete(w.emails, e)
			continue
		}
		if now.Before(a.modeUntil) {
			continue
		}
		if oldest == "" || a.start.Before(w.emails[oldest].start) {
			oldest = e
		}
	}
	if len(w.emails) >= w.maxEmails && oldest != "" {
		delete(w.emails, oldest)
	}
}
