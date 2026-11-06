package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// switchTo moves the session of refresh to tenant, token in the body.
func (c client) switchTo(refresh, tenant string) resp {
	return c.do("POST", "/v1/auth/switch-tenant", "", map[string]any{"tenantId": tenant, "refreshToken": refresh, "tokenDelivery": "body"})
}

func (c client) refreshBody(refresh string) resp {
	return c.do("POST", "/v1/auth/refresh", "", map[string]any{"refreshToken": refresh, "tokenDelivery": "body"})
}

// A person in two companies switches between them with the session she
// has, no password (someone who signed in by magic link has none). The
// refresh token rotates within the same family, so a stolen old one still
// revokes everything; a company she is not in leaves the token untouched.
func TestSwitchTenantWithoutPassword(t *testing.T) {
	deps := newDeps(t)
	origins, err := httpx.ParseOrigins(portalOrigin, false)
	if err != nil {
		t.Fatal(err)
	}
	deps.CORSOrigins = origins
	c := client{t, serve(t, deps)}
	ana := c.registerAndLogin("molino-sw-a", "ana@sw.test")    // owner of A
	beto := c.registerAndLogin("molino-sw-b", "beto@sw.test")  // owner of B
	other := c.registerAndLogin("molino-sw-c", "otro@sw.test") // a company Ana is not in
	if r := c.do("POST", "/v1/tenant/members", beto.access, map[string]any{"email": ana.email, "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("add Ana to B: %d %s", r.status, r.raw)
	}
	if r := c.join(ana.email, nil); r.status != 200 {
		t.Fatalf("Ana accepts B: %d %s", r.status, r.raw)
	}

	// Into B: approver there, and the access token works on B.
	s := c.switchTo(ana.refresh, beto.tenant)
	if s.status != 200 || s.str("tenantId") != beto.tenant || s.str("role") != "approver" || s.str("refreshToken") == "" {
		t.Fatalf("switch to B: %d %s", s.status, s.raw)
	}
	if me := c.do("GET", "/v1/me", s.str("accessToken"), nil); me.str("activeTenantId") != beto.tenant || me.str("role") != "approver" {
		t.Fatalf("me after the switch: %s", me.raw)
	}
	// Refreshing keeps her in B; switching back puts her in A as owner.
	r := c.refreshBody(s.str("refreshToken"))
	if r.status != 200 || r.str("tenantId") != beto.tenant {
		t.Fatalf("refresh after the switch: %d %s", r.status, r.raw)
	}
	back := c.switchTo(r.str("refreshToken"), ana.tenant)
	if back.status != 200 || back.str("tenantId") != ana.tenant || back.str("role") != "owner" {
		t.Fatalf("switch back to A: %d %s", back.status, back.raw)
	}

	// Same family: replaying the token she switched with revokes it all.
	if re := c.switchTo(ana.refresh, beto.tenant); re.status != 401 || re.str("code") != "refresh_token_reused" {
		t.Fatalf("replayed token: %d %s", re.status, re.raw)
	}
	if after := c.refreshBody(back.str("refreshToken")); after.status != 401 {
		t.Fatalf("the newest token survived the replay: %d %s", after.status, after.raw)
	}

	// A company she is not in: 403, and her token is not consumed.
	fresh := c.do("POST", "/v1/auth/login", "", map[string]any{"email": ana.email, "password": ana.password, "tokenDelivery": "body"})
	if no := c.switchTo(fresh.str("refreshToken"), other.tenant); no.status != 403 || no.str("code") != "not_a_member" {
		t.Fatalf("switch into a foreign company: %d %s", no.status, no.raw)
	}
	if ok := c.refreshBody(fresh.str("refreshToken")); ok.status != 200 || ok.str("tenantId") != ana.tenant {
		t.Fatalf("the refused switch consumed the token: %d %s", ok.status, ok.raw)
	}

	// Audited in the company she moved into, with from and to.
	var n int
	if err := deps.DB.Pool.QueryRow(context.Background(), `
		select count(*) from audit_log where action = 'identity.tenant_switched' and tenant_id = $1
		   and detail->>'from' = $2 and detail->>'to' = $1::text`, beto.tenant, ana.tenant).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows A to B: %d %v", n, err)
	}
}

// By cookie, the F22 Origin rule applies, as on refresh and logout.
func TestSwitchTenantByCookieNeedsAnAllowedOrigin(t *testing.T) {
	c := serveWithPortal(t)
	ana := c.registerAndLogin("molino-swc", "ana@swc.test")
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": ana.email, "password": ana.password})
	cookie := strings.SplitN(l.header.Get("Set-Cookie"), ";", 2)[0]
	body := map[string]any{"tenantId": ana.tenant}
	if r := c.do("POST", "/v1/auth/switch-tenant", "", body, "Cookie", cookie, "Origin", "https://evil.test"); r.status != 403 || r.str("code") != "origin_not_allowed" {
		t.Fatalf("foreign origin: %d %s", r.status, r.raw)
	}
	r := c.do("POST", "/v1/auth/switch-tenant", "", body, "Cookie", cookie, "Origin", portalOrigin)
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Set-Cookie"), "tramo_refresh=") {
		t.Fatalf("portal origin: %d %s", r.status, r.raw)
	}
}
