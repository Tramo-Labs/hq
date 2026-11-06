package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

// jobQueue stands in for River: the api only enqueues; run plays the
// worker, as a separate step, like the worker process does.
type jobQueue struct {
	mu      sync.Mutex
	pending []identity.MagicLinkArgs
	worker  *identity.MagicLinkWorker
}

func (q *jobQueue) enqueue(_ context.Context, args river.JobArgs) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, args.(identity.MagicLinkArgs))
	return nil
}

func (q *jobQueue) size() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.pending) }

func (q *jobQueue) run(t *testing.T) {
	t.Helper()
	q.mu.Lock()
	jobs := q.pending
	q.pending = nil
	q.mu.Unlock()
	for _, a := range jobs {
		if err := q.worker.Work(context.Background(), &river.Job[identity.MagicLinkArgs]{Args: a}); err != nil {
			t.Fatal(err)
		}
	}
}

// withMagicLinks wires deps like cmd/api and cmd/worker: mail to an outbox,
// requests enqueued, the worker sending.
func withMagicLinks(t *testing.T, deps app.Deps) (*jobQueue, string) {
	t.Helper()
	outbox := filepath.Join(t.TempDir(), "outbox")
	m := &mail.Outbox{Path: outbox}
	deps.Identity.Mailer, deps.Identity.AppBaseURL = m, "https://app.example.test"
	q := &jobQueue{worker: &identity.MagicLinkWorker{DB: deps.DB, Mailer: m, AppBaseURL: "https://app.example.test"}}
	deps.Identity.Enqueue = q.enqueue
	return q, outbox
}

// magicServer: mail lands in an outbox, the test client plays our Caddy
// (loopback is the trusted proxy, the client IP comes from
// X-Forwarded-For), and limits may be adjusted per test. tokens runs the
// queued jobs first, then reads the outbox.
func magicServer(t *testing.T, adjust func(*identity.Limits)) (client, func() map[string][]string) {
	t.Helper()
	deps := newDeps(t)
	deps.TrustedProxies = loopback
	q, outbox := withMagicLinks(t, deps)
	if adjust != nil {
		adjust(&deps.Identity.Limits)
	}
	c := client{t, serve(t, deps)}
	tokens := func() map[string][]string {
		q.run(t)
		b, err := os.ReadFile(outbox)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out := map[string][]string{}
		for _, msg := range strings.Split(string(b), "From tramo ") {
			if m := tokenInMail.FindStringSubmatch(msg); m != nil {
				out[m[1]] = append(out[m[1]], m[2])
			}
		}
		return out
	}
	return c, tokens
}

func (c client) loginFrom(ip, email, password string) resp {
	return c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": password, "tokenDelivery": "body"},
		"X-Forwarded-For", ip)
}

func (c client) askLink(ip, email string) resp {
	return c.do("POST", "/v1/auth/magic-link", "", map[string]any{"email": email}, append(idem(), "X-Forwarded-For", ip)...)
}

func (c client) acceptLink(ip, token string) resp {
	return c.do("POST", "/v1/auth/magic-link/accept", "", map[string]any{"token": token, "tokenDelivery": "body"},
		"X-Forwarded-For", ip)
}

// D-17: many failures on one email from many IPs never lock it out for
// everyone. The email switches to magic link: the password is not even
// checked (same answer right or wrong, so the mode is not an oracle), and
// the owner gets in through the link.
func TestDistributedGuessingSwitchesToMagicLinkNotLockout(t *testing.T) {
	c, tokens := magicServer(t, nil)
	own := c.registerAndLogin("molino-d17", "owner@d17.test")

	for i := 0; i < 20; i++ {
		if r := c.loginFrom("192.0.2."+strconv.Itoa(i+1), own.email, "guess"); r.status != 401 || r.str("code") != "unauthorized" {
			t.Fatalf("guess %d: %d %s", i+1, r.status, r.raw)
		}
	}
	right := c.loginFrom("198.51.100.1", own.email, own.password)
	wrong := c.loginFrom("198.51.100.2", own.email, "guess")
	for name, r := range map[string]resp{"right password": right, "wrong password": wrong} {
		if r.status != 401 || r.str("code") != "magic_link_required" {
			t.Errorf("%s under attack: %d %s", name, r.status, r.raw)
		}
	}
	strip := func(r resp) string {
		delete(r.body, "requestId")
		return strings.Join([]string{r.str("code"), r.str("detail")}, "|")
	}
	if strip(right) != strip(wrong) || len(right.body) != len(wrong.body) {
		t.Errorf("the mode tells right from wrong: %v vs %v", right.body, wrong.body)
	}

	// The owner always has a way in.
	if r := c.askLink("198.51.100.1", own.email); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	toks := tokens()[own.email]
	if len(toks) != 1 {
		t.Fatalf("mailed links: %v", tokens())
	}
	s := c.acceptLink("198.51.100.1", toks[0])
	if s.status != 200 || s.str("accessToken") == "" || s.str("role") != "owner" {
		t.Fatalf("accept link: %d %s", s.status, s.raw)
	}
	if strings.Contains(s.raw, toks[0]) {
		t.Fatal("the session response echoes the link token")
	}
	if r := c.acceptLink("198.51.100.1", toks[0]); r.status != 401 || r.str("code") != "invalid_magic_link" {
		t.Fatalf("second use of the link: %d %s", r.status, r.raw)
	}
	if r := c.acceptLink("198.51.100.1", strings.Repeat("A", 43)); r.status != 401 || r.str("code") != "invalid_magic_link" {
		t.Fatalf("unknown link: %d %s", r.status, r.raw)
	}

	// Another email, attacked from the same IPs, is untouched.
	other := c.registerAndLogin("molino-d17b", "other@d17.test")
	if r := c.loginFrom("192.0.2.1", other.email, other.password); r.status != 200 {
		t.Fatalf("another email: %d %s", r.status, r.raw)
	}
}

// The mode needs failures from at least three distinct IPs, and twenty of
// them: one or two IPs are the pair throttle's job, never the email's.
func TestMagicLinkModeNeedsThreeIPsAndTwentyFailures(t *testing.T) {
	c, _ := magicServer(t, func(l *identity.Limits) {
		l.FailPerEmailIP = ratelimit.Rule{Limit: 1000, Window: 15 * time.Minute} // isolate the email rule
	})
	own := c.registerAndLogin("molino-ips", "owner@ips.test")
	for i := 0; i < 30; i++ {
		c.loginFrom("192.0.2."+strconv.Itoa(1+i%2), own.email, "guess")
	}
	if r := c.loginFrom("198.51.100.1", own.email, own.password); r.status != 200 {
		t.Fatalf("30 failures from 2 IPs switched the email: %d %s", r.status, r.raw)
	}
	b := c.registerAndLogin("molino-ips-b", "b@ips.test")
	for i := 0; i < 19; i++ {
		c.loginFrom("192.0.2."+strconv.Itoa(1+i%5), b.email, "guess")
	}
	if r := c.loginFrom("198.51.100.1", b.email, b.password); r.status != 200 {
		t.Fatalf("19 failures switched the email: %d %s", r.status, r.raw)
	}
	c.loginFrom("192.0.2.9", b.email, "guess")
	if r := c.loginFrom("198.51.100.1", b.email, b.password); r.status != 401 || r.str("code") != "magic_link_required" {
		t.Fatalf("20 failures from 6 IPs: %d %s", r.status, r.raw)
	}
}

// The pair's 429 says a magic link still works; the per IP 429 (spraying)
// does not. A link accepted from that IP clears the pair's failures.
func TestPairThrottleOffersTheMagicLink(t *testing.T) {
	c, tokens := magicServer(t, nil)
	own := c.registerAndLogin("molino-pair", "owner@pair.test")
	for i := 0; i < 5; i++ {
		c.loginFrom("203.0.113.66", own.email, "guess")
	}
	r := c.loginFrom("203.0.113.66", own.email, own.password)
	if r.status != 429 || r.str("code") != "rate_limited" || r.body["magicLink"] != true || r.header.Get("Retry-After") == "" {
		t.Fatalf("pair 429: %d %s", r.status, r.raw)
	}
	if r := c.askLink("203.0.113.66", own.email); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	if r := c.acceptLink("203.0.113.66", tokens()[own.email][0]); r.status != 200 {
		t.Fatalf("accept: %d %s", r.status, r.raw)
	}
	if r := c.loginFrom("203.0.113.66", own.email, own.password); r.status != 200 {
		t.Fatalf("after the link the pair is clear: %d %s", r.status, r.raw)
	}

	spray := resp{}
	for i := 0; i < 31; i++ {
		spray = c.loginFrom("203.0.113.99", "x"+strconv.Itoa(i)+"@pair.test", "guess")
	}
	if spray.status != 429 || spray.body["magicLink"] != nil {
		t.Fatalf("per IP 429: %d %s", spray.status, spray.raw)
	}
}

// Asking for a link never tells whether the email has an account, needs an
// Idempotency-Key, and is throttled per email (sending) and per IP.
func TestAskingForAMagicLink(t *testing.T) {
	c, tokens := magicServer(t, nil)
	own := c.registerAndLogin("molino-ask", "owner@ask.test")

	known, unknown := c.askLink("198.51.100.1", own.email), c.askLink("198.51.100.1", "nadie@ask.test")
	if known.status != 202 || unknown.status != 202 || known.raw != unknown.raw {
		t.Fatalf("known %d %q, unknown %d %q", known.status, known.raw, unknown.status, unknown.raw)
	}
	if got := tokens(); len(got[own.email]) != 1 || len(got["nadie@ask.test"]) != 0 {
		t.Fatalf("mailed: %v", got)
	}
	if r := c.do("POST", "/v1/auth/magic-link", "", map[string]any{"email": own.email}); r.status != 400 || r.str("code") != "idempotency_key_required" {
		t.Fatalf("without Idempotency-Key: %d %s", r.status, r.raw)
	}

	// Three sends per email per 15 minutes, from any IP; known or not.
	for _, email := range []string{own.email, "nadie@ask.test"} {
		for i := 0; i < 2; i++ {
			if r := c.askLink("198.51.100."+strconv.Itoa(10+i), email); r.status != 202 {
				t.Fatalf("%s send %d: %d", email, i+2, r.status)
			}
		}
		if r := c.askLink("198.51.100.20", email); r.status != 429 || r.header.Get("Retry-After") == "" {
			t.Fatalf("%s 4th send: %d %s", email, r.status, r.raw)
		}
	}
	if n := len(tokens()[own.email]); n != 3 {
		t.Fatalf("%d links mailed, want 3", n)
	}

	// One IP cannot ask for links for many emails without limit.
	refused := 0
	for i := 0; i < 12; i++ {
		if r := c.askLink("203.0.113.5", "s"+strconv.Itoa(i)+"@ask.test"); r.status == 429 {
			refused++
		}
	}
	if refused != 2 {
		t.Fatalf("per IP: %d of 12 refused, want 2", refused)
	}
}

// Asking for a link must not tell, even by timing, whether the email has an
// account. Not by measuring times: by checking that the request does the
// same work for both, and that everything that depends on the email
// existing (the lookup, the link row, the outbox) happens in the job.
func TestAskingDoesTheSameWorkForKnownAndUnknownEmails(t *testing.T) {
	deps := newDeps(t)
	q, outbox := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-same", "owner@same.test")

	links := func() int {
		var n int
		if err := deps.DB.Pool.QueryRow(context.Background(), `select count(*) from magic_links`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, email := range []string{own.email, "nadie@same.test"} {
		r := c.askLink("198.51.100.1", email)
		if r.status != 202 || r.raw != "" {
			t.Fatalf("%s: %d %q", email, r.status, r.raw)
		}
		if _, err := os.Stat(outbox); !os.IsNotExist(err) {
			t.Fatalf("%s: the request wrote the outbox", email)
		}
		if n := links(); n != 0 {
			t.Fatalf("%s: the request stored a link", email)
		}
	}
	// One stored request and one job per request, the same for both: the
	// job carries only the request's id.
	q.mu.Lock()
	got := append([]identity.MagicLinkArgs(nil), q.pending...)
	q.mu.Unlock()
	if len(got) != 2 || got[0].Request == got[1].Request {
		t.Fatalf("jobs %+v, want one per request", got)
	}
	requests := func() int {
		var n int
		if err := deps.DB.Pool.QueryRow(context.Background(), `select count(*) from magic_link_requests`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := requests(); n != 2 {
		t.Fatalf("%d stored requests, want one per request, known or not", n)
	}

	q.run(t)
	if n := links(); n != 1 {
		t.Fatalf("%d links after the jobs, want 1 (known email only)", n)
	}
	if n := requests(); n != 0 {
		t.Fatalf("%d requests left after the jobs", n)
	}
	if b, _ := os.ReadFile(outbox); strings.Contains(string(b), "nadie@same.test") || !strings.Contains(string(b), own.email) {
		t.Fatalf("outbox after the jobs: %s", b)
	}
	if q.size() != 0 {
		t.Fatal("jobs left")
	}
}

// A link works for 15 minutes; after that it is refused like an unknown one.
func TestAnExpiredMagicLinkIsRefused(t *testing.T) {
	deps := newDeps(t)
	q, outbox := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-exp", "owner@exp.test")
	if r := c.askLink("198.51.100.1", own.email); r.status != 202 {
		t.Fatalf("ask: %d %s", r.status, r.raw)
	}
	q.run(t)
	b, _ := os.ReadFile(outbox)
	m := tokenInMail.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no link mailed: %s", b)
	}
	var ttl time.Duration
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select expires_at - created_at from magic_links`).Scan(&ttl); err != nil || ttl.Round(time.Second) != 15*time.Minute {
		t.Fatalf("link lifetime %v, %v", ttl, err)
	}
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`update magic_links set expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if r := c.acceptLink("198.51.100.1", m[2]); r.status != 401 || r.str("code") != "invalid_magic_link" {
		t.Fatalf("expired link: %d %s", r.status, r.raw)
	}
}
