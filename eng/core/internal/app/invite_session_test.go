package app_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

func acceptAs(c client, bearer, token string, extra map[string]any) resp {
	body := map[string]any{"token": token, "tokenDelivery": "body"}
	for k, v := range extra {
		body[k] = v
	}
	return c.do("POST", "/v1/auth/invite/accept", bearer, body)
}

// F41: an accountant signed in accepts a second invitation without closing
// the session. With the access token of the account it is for: 204, the
// membership is active, no session is issued and the signed in one keeps
// its tenant; a password sent along is ignored.
func TestAcceptingWithTheSameAccountKeepsTheSession(t *testing.T) {
	c := client{t, server(t)}
	alfa := c.registerAndLogin("alfa-f41", "owner@alfa-f41.test")
	acc := c.member(alfa, alfa.tenant, "acc-f41", "contador@f41.test", "viewer")
	beta := c.registerAndLogin("beta-f41", "owner@beta-f41.test")
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": acc.email, "role": "accountant"}, idem()...); r.status != 201 {
		t.Fatalf("beta invites: %d %s", r.status, r.raw)
	}
	toks := c.invitesTo(acc.email)
	r := acceptAs(c, acc.access, toks[len(toks)-1], map[string]any{"password": "an attempt to change it"})
	if r.status != 204 || r.raw != "" || r.header.Get("Set-Cookie") != "" {
		t.Fatalf("accept with the same account: %d %q %v", r.status, r.raw, r.header)
	}
	if me := c.do("GET", "/v1/me", acc.access, nil); !strings.Contains(tenantsOf(me), "beta-f41") || me.str("activeTenantId") != acc.tenant {
		t.Fatalf("after accepting: %s", me.raw)
	}
	s := c.refreshBody(acc.refresh)
	if s.status != 200 || s.str("tenantId") != acc.tenant {
		t.Fatalf("the signed in session moved: %d %s", s.status, s.raw)
	}
	if sw := c.switchTo(s.str("refreshToken"), beta.tenant); sw.status != 200 || sw.str("role") != "accountant" {
		t.Fatalf("switch to the accepted company: %d %s", sw.status, sw.raw)
	}
	if l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": acc.email, "password": "an attempt to change it", "tokenDelivery": "body"}); l.status != 401 {
		t.Fatalf("the password sent along was set: %d", l.status)
	}
}

// With a session of another account the invitation is not accepted: 409
// invite_for_another_account, no tokens, nothing used, audited as a
// rejection, and the person it is for accepts it afterwards.
func TestAcceptingWithAnotherAccountIsRefused(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-f41o", "owner@f41o.test")
	other := c.registerAndLogin("otro-f41o", "otro@f41o.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@f41o.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := c.invitesTo("nueva@f41o.test")[0]
	r := acceptAs(c, other.access, tok, map[string]any{"password": "a long enough password"})
	if r.status != 409 || r.str("code") != "invite_for_another_account" || strings.Contains(r.raw, "Token") {
		t.Fatalf("accept with another account: %d %s", r.status, r.raw)
	}
	var audited int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'identity.invite_rejected' and detail->>'reason' = 'another account'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("%d rejections for another account audited", audited)
	}
	if r := accept(c, tok, "a long enough password"); r.status != 200 || r.str("tenantId") != own.tenant {
		t.Fatalf("the invitation after the refusal: %d %s", r.status, r.raw)
	}
}

// An account with no password (in by magic link) accepts with its session
// without being asked for one, and none is set.
func TestAcceptingWithASessionNeedsNoPassword(t *testing.T) {
	deps := newDeps(t)
	deps.TrustedProxies = loopback
	q, outbox := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	alfa := c.registerAndLogin("alfa-f41m", "owner@alfa-f41m.test")
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`with u as (insert into users (email) values ('link@f41m.test') returning id)
		 insert into memberships (user_id, tenant_id, role) select id, $1, 'viewer' from u`, alfa.tenant); err != nil {
		t.Fatal(err)
	}
	if r := c.askLink("198.51.100.41", "link@f41m.test"); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	q.run(t)
	b, err := os.ReadFile(outbox) // #nosec G304 -- this test's outbox
	if err != nil {
		t.Fatal(err)
	}
	m := linkInMail.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no link mailed:\n%s", b)
	}
	s := c.acceptLink("198.51.100.41", m[2])
	if s.status != 200 {
		t.Fatalf("magic link: %d %s", s.status, s.raw)
	}
	beta := c.registerAndLogin("beta-f41m", "owner@beta-f41m.test")
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": "link@f41m.test", "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("beta invites: %d %s", r.status, r.raw)
	}
	if r := acceptAs(c, s.str("accessToken"), c.invitesTo("link@f41m.test")[0], nil); r.status != 204 {
		t.Fatalf("accept with a session and no password: %d %s", r.status, r.raw)
	}
	var set bool
	if err := deps.DB.Pool.QueryRow(context.Background(), `select password_hash is not null from users where email = 'link@f41m.test'`).Scan(&set); err != nil || set {
		t.Fatalf("a password was set (%v)", err)
	}
}

// An invalid bearer is 401 unauthorized before the invitation is looked
// at: it spends nothing of the cap.
func TestAnInvalidBearerOnAcceptSpendsNothing(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 1, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-f41b", "owner@f41b.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@f41b.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := c.invitesTo("nueva@f41b.test")[0]
	for i := 0; i < 3; i++ {
		if r := acceptAs(c, "not-a-token", tok, nil); r.status != 401 || r.str("code") != "unauthorized" {
			t.Fatalf("invalid bearer %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := accept(c, tok, "a long enough password"); r.status != 200 {
		t.Fatalf("accept after invalid bearers: %d %s", r.status, r.raw)
	}
}
