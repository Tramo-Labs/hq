package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/app"
)

// client is a tiny JSON client for the end to end tests.
type client struct {
	t   *testing.T
	srv *httptest.Server
}

type resp struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (c client) do(method, path, token string, body any, headers ...string) resp {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (r resp) str(k string) string { s, _ := r.body[k].(string); return s }

func idem() []string { return []string{"Idempotency-Key", uuid.NewString()} }

type account struct {
	email, password, tenant, access, refresh string
}

func (c client) registerAndLogin(slug, email string) account {
	c.t.Helper()
	a := account{email: email, password: "a long enough password"}
	r := c.do("POST", "/v1/auth/register", "", map[string]any{
		"email": email, "password": a.password, "tenantName": "Molino " + slug, "tenantSlug": slug,
	}, idem()...)
	if r.status != 201 {
		c.t.Fatalf("register %s: %d %s", email, r.status, r.raw)
	}
	a.tenant = r.str("tenantId")
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": a.password, "tokenDelivery": "body"})
	if l.status != 200 {
		c.t.Fatalf("login %s: %d %s", email, l.status, l.raw)
	}
	a.access, a.refresh = l.str("accessToken"), l.str("refreshToken")
	return a
}

func TestRegisterIsIdempotentAndValidated(t *testing.T) {
	c := client{t, server(t)}
	body := map[string]any{"email": "ana@molino.test", "password": "a long enough password",
		"tenantName": "Molino del Sur", "tenantSlug": "molino-sur", "cuit": "30000000015"}

	if r := c.do("POST", "/v1/auth/register", "", body); r.status != 400 || r.str("code") != "idempotency_key_required" {
		t.Fatalf("without key: %d %s", r.status, r.raw)
	}
	key := idem()
	first := c.do("POST", "/v1/auth/register", "", body, key...)
	again := c.do("POST", "/v1/auth/register", "", body, key...)
	if first.status != 201 || again.status != 201 || first.raw != again.raw || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %s / %d %s", first.status, first.raw, again.status, again.raw)
	}
	if strings.Contains(first.raw, "Token") {
		t.Fatal("register response carries a token (it is stored for idempotency)")
	}
	body2 := map[string]any{"email": "otra@molino.test", "password": "a long enough password", "tenantName": "Otro molino", "tenantSlug": "otro"}
	if r := c.do("POST", "/v1/auth/register", "", body2, key...); r.status != 422 {
		t.Fatalf("same key other body: %d", r.status)
	}

	for name, tc := range map[string]struct {
		patch  map[string]any
		status int
		code   string
	}{
		"email taken":    {map[string]any{"tenantSlug": "otro-1", "cuit": nil}, 409, "email_taken"},
		"slug taken":     {map[string]any{"email": "b@molino.test", "cuit": nil}, 409, "slug_taken"},
		"cuit taken":     {map[string]any{"email": "c@molino.test", "tenantSlug": "otro-2"}, 409, "cuit_taken"},
		"bad cuit":       {map[string]any{"email": "d@molino.test", "tenantSlug": "otro-3", "cuit": "30000000016"}, 400, "invalid_cuit"},
		"short password": {map[string]any{"email": "e@molino.test", "tenantSlug": "otro-4", "cuit": nil, "password": "short"}, 400, "bad_request"},
		"unknown field":  {map[string]any{"email": "f@molino.test", "tenantSlug": "otro-5", "cuit": nil, "clave_fiscal": "x"}, 400, "bad_request"},
	} {
		b := map[string]any{}
		for k, v := range body {
			b[k] = v
		}
		for k, v := range tc.patch {
			if v == nil {
				delete(b, k)
			} else {
				b[k] = v
			}
		}
		if r := c.do("POST", "/v1/auth/register", "", b, idem()...); r.status != tc.status || r.str("code") != tc.code {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
}

func TestLoginAndAccessToken(t *testing.T) {
	c := client{t, server(t)}
	a := c.registerAndLogin("alfa", "ana@alfa.test")

	wrong := c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": "not the password"})
	unknown := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "nadie@alfa.test", "password": "not the password"})
	if wrong.status != 401 || unknown.status != 401 || wrong.str("detail") != unknown.str("detail") {
		t.Fatalf("bad logins must look the same: %s / %s", wrong.raw, unknown.raw)
	}

	// Browser login: refresh only in an HttpOnly cookie.
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": a.password})
	cookie := l.header.Get("Set-Cookie")
	for _, attr := range []string{"tramo_refresh=", "Path=/v1/auth", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(cookie, attr) {
			t.Errorf("cookie lacks %s: %s", attr, cookie)
		}
	}
	if l.str("refreshToken") != "" || l.body["expiresIn"] != float64(600) || l.str("role") != "owner" {
		t.Fatalf("browser login body: %s", l.raw)
	}

	me := c.do("GET", "/v1/me", a.access, nil)
	if me.status != 200 || me.str("email") != a.email || me.str("activeTenantId") != a.tenant {
		t.Fatalf("me: %d %s", me.status, me.raw)
	}
	if r := c.do("GET", "/v1/me", "", nil); r.status != 401 || r.header.Get("WWW-Authenticate") == "" {
		t.Fatalf("me without token: %d", r.status)
	}
	if r := c.do("GET", "/v1/me", a.access+"x", nil); r.status != 401 {
		t.Fatalf("me with tampered token: %d", r.status)
	}
}

func TestRefreshRotationAndReuseRevokesFamily(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("alfa", "ana@alfa.test")

	refresh := func(tok string) resp {
		return c.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": tok, "tokenDelivery": "body"})
	}
	r1 := a.refresh
	second := refresh(r1)
	if second.status != 200 || second.str("refreshToken") == r1 || second.str("accessToken") == "" {
		t.Fatalf("rotation: %d %s", second.status, second.raw)
	}
	r2 := second.str("refreshToken")
	third := refresh(r2)
	if third.status != 200 {
		t.Fatalf("second rotation: %d", third.status)
	}
	r3 := third.str("refreshToken")

	// r1 was already used: theft assumed, the whole family dies.
	if r := refresh(r1); r.status != 401 || r.str("code") != "refresh_token_reused" {
		t.Fatalf("reuse: %d %s", r.status, r.raw)
	}
	if r := refresh(r3); r.status != 401 {
		t.Fatalf("newest token of a revoked family still works: %d", r.status)
	}
	// A new login is a new family and works.
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": a.password, "tokenDelivery": "body"})
	if r := refresh(l.str("refreshToken")); r.status != 200 {
		t.Fatalf("fresh family after reuse: %d", r.status)
	}

	// Stored as a hash, never the token itself; the reuse is audited.
	ctx := context.Background()
	var clear, reuse int
	err := deps.DB.System(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `select count(*) from refresh_tokens where token_hash = convert_to($1, 'UTF8')`, r1).Scan(&clear); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `select count(*) from audit_log where action = 'identity.refresh_reuse'`).Scan(&reuse)
	})
	if err != nil {
		t.Fatal(err)
	}
	if clear != 0 || reuse != 1 {
		t.Fatalf("plain tokens stored: %d, reuse audit rows: %d", clear, reuse)
	}
}

func TestRefreshByCookieAndLogout(t *testing.T) {
	c := client{t, server(t)}
	a := c.registerAndLogin("alfa", "ana@alfa.test")
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": a.password})
	cookie := strings.SplitN(l.header.Get("Set-Cookie"), ";", 2)[0]

	r := c.do("POST", "/v1/auth/refresh", "", nil, "Cookie", cookie)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Set-Cookie"), "tramo_refresh=") {
		t.Fatalf("cookie refresh: %d %s", r.status, r.raw)
	}
	next := strings.SplitN(r.header.Get("Set-Cookie"), ";", 2)[0]
	out := c.do("POST", "/v1/auth/logout", "", nil, "Cookie", next)
	if out.status != 204 || !strings.Contains(out.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %v", out.status, out.header)
	}
	if r := c.do("POST", "/v1/auth/refresh", "", nil, "Cookie", next); r.status != 401 {
		t.Fatalf("refresh after logout: %d", r.status)
	}
}

func TestTenantsAreIsolatedAndRolesEnforced(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("alfa", "ana@alfa.test")
	b := c.registerAndLogin("beta", "beto@beta.test")

	members := func(token string) []any {
		r := c.do("GET", "/v1/tenant/members", token, nil)
		if r.status != 200 {
			t.Fatalf("members: %d %s", r.status, r.raw)
		}
		items, _ := r.body["items"].([]any)
		return items
	}
	if got := members(a.access); len(got) != 1 || strings.Contains(c.do("GET", "/v1/tenant/members", a.access, nil).raw, b.email) {
		t.Fatalf("tenant a sees: %v", got)
	}

	// The owner adds an approver and a viewer; adding twice conflicts.
	add := func(token, email, role string) resp {
		return c.do("POST", "/v1/tenant/members", token, map[string]any{"email": email, "role": role}, idem()...)
	}
	if r := add(a.access, "apro@alfa.test", "approver"); r.status != 201 {
		t.Fatalf("add approver: %d %s", r.status, r.raw)
	}
	if r := add(a.access, "apro@alfa.test", "viewer"); r.status != 409 {
		t.Fatalf("duplicate member: %d", r.status)
	}
	// Nobody grants operator over HTTP (F7; full matrix in authz_test.go).
	if r := add(a.access, "x@tramo.test", "operator"); r.status != 403 {
		t.Fatalf("owner granted operator: %d", r.status)
	}
	if r := c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "y@alfa.test", "role": "viewer"}); r.status != 400 {
		t.Fatalf("add member without Idempotency-Key: %d", r.status)
	}
	if got := members(a.access); len(got) != 2 {
		t.Fatalf("tenant a members after add: %d", len(got))
	}
	if got := members(b.access); len(got) != 1 {
		t.Fatalf("tenant b sees %d members", len(got))
	}

	// A person in both tenants: memberships list both, the token only one.
	if r := add(b.access, a.email, "viewer"); r.status != 201 {
		t.Fatalf("cross membership: %d %s", r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": a.password, "tenantId": b.tenant, "tokenDelivery": "body"})
	if l.status != 200 || l.str("role") != "viewer" || l.str("tenantId") != b.tenant {
		t.Fatalf("login into tenant b: %d %s", l.status, l.raw)
	}
	me := c.do("GET", "/v1/me", l.str("accessToken"), nil)
	if ms, _ := me.body["memberships"].([]any); len(ms) != 2 {
		t.Fatalf("memberships: %s", me.raw)
	}
	// As a viewer in b, ana cannot add people.
	if r := add(l.str("accessToken"), "z@beta.test", "viewer"); r.status != 403 {
		t.Fatalf("viewer added a member: %d", r.status)
	}
	// Removing the membership ends the session at the next refresh.
	ctx := context.Background()
	if _, err := deps.DB.Pool.Exec(ctx, `delete from memberships where tenant_id = $1 and user_id = (select id from users where email = $2)`, b.tenant, a.email); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": l.str("refreshToken")}); r.status != 401 {
		t.Fatalf("refresh after membership removal: %d", r.status)
	}
}

func TestIdentityIsAudited(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	a := c.registerAndLogin("alfa", "ana@alfa.test")
	c.do("POST", "/v1/auth/login", "", map[string]any{"email": a.email, "password": "not the password"})

	ctx := context.Background()
	got := map[string]int{}
	err := deps.DB.System(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `select action, count(*) from audit_log where request_id is not null and ip is not null group by action`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var action string
			var n int
			if err := rows.Scan(&action, &n); err != nil {
				return err
			}
			got[action] = n
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"identity.register", "identity.login", "identity.login_failed"} {
		if got[want] != 1 {
			t.Errorf("audit %s: %d rows (all: %v)", want, got[want], got)
		}
	}
	var leaked int
	_ = deps.DB.System(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from audit_log where detail::text ilike '%password%'`).Scan(&leaked)
	})
	if leaked != 0 {
		t.Fatal("password in the audit log")
	}
}

func TestLoginAndRegisterAreThrottled(t *testing.T) {
	deps := newDeps(t)
	// The test client plays our Caddy: its loopback address is the trusted
	// proxy, so the client IP comes from X-Forwarded-For.
	deps.TrustedProxies = loopback
	c := client{t, serve(t, deps)}
	from := func(ip string) []string { return []string{"X-Forwarded-For", ip} }
	login := func(ip, email, password string) resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": email, "password": password, "tokenDelivery": "body"}, from(ip)...)
	}
	r := c.do("POST", "/v1/auth/register", "", map[string]any{
		"email": "ana@alfa.test", "password": "a long enough password", "tenantName": "Molino alfa", "tenantSlug": "alfa",
	}, append(idem(), from("198.51.100.1")...)...)
	if r.status != 201 {
		t.Fatalf("register: %d %s", r.status, r.raw)
	}

	// Guessing from one IP: five failures, then even the right password waits.
	for i := 0; i < 5; i++ {
		if r := login("203.0.113.66", "ana@alfa.test", "guess "+string(rune('a'+i))); r.status != 401 {
			t.Fatalf("guess %d: %d", i+1, r.status)
		}
	}
	blocked := login("203.0.113.66", "ana@alfa.test", "a long enough password")
	if blocked.status != 429 || blocked.str("code") != "rate_limited" || blocked.header.Get("Retry-After") == "" {
		t.Fatalf("6th attempt from the guessing IP: %d %v %s", blocked.status, blocked.header.Get("Retry-After"), blocked.raw)
	}
	// The owner, from her own IP, is not locked out by that attacker.
	if r := login("198.51.100.1", "ana@alfa.test", "a long enough password"); r.status != 200 {
		t.Fatalf("owner from another IP: %d %s", r.status, r.raw)
	}

	// Distributed guessing: twenty failures from twenty IPs throttle the email.
	for i := 0; i < 20; i++ {
		login("192.0.2."+strconv.Itoa(i+1), "beto@alfa.test", "guess")
	}
	if r := login("192.0.2.200", "beto@alfa.test", "guess"); r.status != 429 {
		t.Fatalf("distributed guessing not throttled: %d", r.status)
	}

	// One IP cannot spray more than 30 logins a minute, whatever the emails.
	sprayed := 0
	for i := 0; i < 40; i++ {
		if r := login("203.0.113.99", "x"+strconv.Itoa(i)+"@alfa.test", "guess"); r.status == 429 {
			sprayed++
		}
	}
	if sprayed != 10 {
		t.Fatalf("spray from one IP: %d of 40 throttled, want 10", sprayed)
	}

	// Registrations: ten per IP per hour.
	got429 := 0
	for i := 0; i < 11; i++ {
		r := c.do("POST", "/v1/auth/register", "", map[string]any{
			"email": "r" + strconv.Itoa(i) + "@spam.test", "password": "a long enough password",
			"tenantName": "Spam " + strconv.Itoa(i), "tenantSlug": "spam-" + strconv.Itoa(i),
		}, append(idem(), from("203.0.113.7")...)...)
		if r.status == 429 {
			got429++
		}
	}
	if got429 != 1 {
		t.Fatalf("register throttle: %d of 11 refused, want 1", got429)
	}
}

var loopback = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}

// A client that reaches the api directly cannot dodge the per IP throttle by
// writing its own X-Forwarded-For: only the configured proxy is believed.
func TestSpoofedForwardedForDoesNotDodgeThrottles(t *testing.T) {
	deps := newDeps(t)
	deps.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.1/32")} // our Caddy, not the test client
	c := client{t, serve(t, deps)}
	refused := 0
	for i := 0; i < 40; i++ {
		r := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "x" + strconv.Itoa(i) + "@alfa.test", "password": "guess"},
			"X-Forwarded-For", "198.51.100."+strconv.Itoa(i+1)) // a new "IP" every time
		if r.status == 429 {
			refused++
		}
	}
	if refused != 10 {
		t.Fatalf("rotating X-Forwarded-For from a direct client: %d of 40 refused, want 10 (30 per minute per real IP)", refused)
	}
}

// The web's Next server proxies /v1 same origin, so every login reaches
// the core from its address. With the web in TRUSTED_PROXIES and the
// browser's address in X-Forwarded-For, two people behind it are throttled
// separately: one guessing a password does not lock out the other.
func TestClientsBehindTheWebProxyCountSeparately(t *testing.T) {
	login := func(c client, ip, password string) int {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": "owner@proxy.test", "password": password},
			"X-Forwarded-For", ip).status
	}
	run := func(proxies []netip.Prefix) (attacker, owner int) {
		deps := newDeps(t)
		deps.TrustedProxies = proxies
		c := client{t, serve(t, deps)}
		c.registerAndLogin("molino-proxy", "owner@proxy.test")
		for i := 0; i < 5; i++ {
			login(c, "203.0.113.66", "wrong password")
		}
		return login(c, "203.0.113.66", "wrong password"), login(c, "198.51.100.7", "a long enough password")
	}
	// The test client is the web server (127.0.0.1), trusted.
	if attacker, owner := run([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}); attacker != 429 || owner != 200 {
		t.Fatalf("web trusted: attacker %d (want 429), owner %d (want 200)", attacker, owner)
	}
	// Not trusted: everyone is the web's address and the owner pays for it.
	if _, owner := run(nil); owner != 429 {
		t.Fatalf("web not trusted: owner %d, want 429 (shared bucket)", owner)
	}
}

var _ = app.Deps{}
