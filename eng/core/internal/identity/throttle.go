package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// Limits are the throttles on the public identity endpoints. They are
// checked before any password hashing, so a flood costs no argon2 time.
type Limits struct {
	// Every login request from one IP (spraying many emails).
	LoginPerIP ratelimit.Rule
	// Failed logins for one email from one IP (guessing one password).
	FailPerEmailIP ratelimit.Rule
	// Failed logins for one email from any IP (distributed guessing). Kept
	// well above FailPerEmailIP so a single attacker cannot lock the owner out.
	FailPerEmail ratelimit.Rule
	// Registrations from one IP.
	RegisterPerIP ratelimit.Rule
}

// DefaultLimits are deliberately loose for people and tight for scripts.
var DefaultLimits = Limits{
	LoginPerIP:     ratelimit.Rule{Limit: 30, Window: time.Minute},
	FailPerEmailIP: ratelimit.Rule{Limit: 5, Window: 15 * time.Minute},
	FailPerEmail:   ratelimit.Rule{Limit: 20, Window: time.Hour},
	RegisterPerIP:  ratelimit.Rule{Limit: 10, Window: time.Hour},
}

// ErrRateLimited is returned when a throttle trips.
var ErrRateLimited = errors.New("too many attempts")

// RateLimitedError carries how long the caller should wait.
type RateLimitedError struct{ RetryAfter time.Duration }

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

// checkLogin counts the request against the IP and refuses when any login
// throttle is already exhausted.
func (s *Service) checkLogin(ctx context.Context, email string) error {
	ip, e := clientIP(ctx), emailKey(email)
	if ok, retry := s.Limiter.Allow("login:ip:"+ip, s.Limits.LoginPerIP); !ok {
		return limited(retry)
	}
	if ex, retry := s.Limiter.Exceeded("login:fail:"+e+"|"+ip, s.Limits.FailPerEmailIP); ex {
		return limited(retry)
	}
	if ex, retry := s.Limiter.Exceeded("login:fail:"+e, s.Limits.FailPerEmail); ex {
		return limited(retry)
	}
	return nil
}

func (s *Service) loginFailed(ctx context.Context, email string) {
	ip, e := clientIP(ctx), emailKey(email)
	s.Limiter.Add("login:fail:"+e+"|"+ip, s.Limits.FailPerEmailIP)
	s.Limiter.Add("login:fail:"+e, s.Limits.FailPerEmail)
}

func (s *Service) loginSucceeded(ctx context.Context, email string) {
	// Only this IP's failures are forgiven; distributed failures on the
	// email keep counting until their window ends.
	s.Limiter.Reset("login:fail:" + emailKey(email) + "|" + clientIP(ctx))
}

func (s *Service) checkRegister(ctx context.Context) error {
	if ok, retry := s.Limiter.Allow("register:ip:"+clientIP(ctx), s.Limits.RegisterPerIP); !ok {
		return limited(retry)
	}
	return nil
}
