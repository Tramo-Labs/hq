package app_test

import (
	"context"
	"strings"
	"testing"
)

func nameOf(r resp) any { return r.body["name"] }

// B: the person's name. Set at registration or later, only by the person,
// trimmed, never empty or with control characters; shown in /me and to
// the members of the same tenant (accountant and operator included), the
// same as the email; audited without the value.
func TestPersonName(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}

	// Registration with a name, trimmed.
	r := c.do("POST", "/v1/auth/register", "", map[string]any{
		"email": "ana@nom.test", "password": "a long enough password", "tenantName": "Molino nom", "tenantSlug": "molino-nom",
		"name": "  Ana Pérez  ",
	}, idem()...)
	if r.status != 201 {
		t.Fatalf("register: %d %s", r.status, r.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "ana@nom.test", "password": "a long enough password", "tokenDelivery": "body"})
	ana := account{email: "ana@nom.test", password: "a long enough password", tenant: r.str("tenantId"), access: l.str("accessToken")}
	if me := c.do("GET", "/v1/me", ana.access, nil); nameOf(me) != "Ana Pérez" {
		t.Fatalf("me after register: %s", me.raw)
	}

	// Without a name: null until set; then the person sets it.
	beto := c.member(ana, ana.tenant, "molino-nom-b", "beto@nom.test", "approver")
	if me := c.do("GET", "/v1/me", beto.access, nil); nameOf(me) != nil || !strings.Contains(me.raw, `"name":null`) {
		t.Fatalf("me without a name: %s", me.raw)
	}
	if p := c.do("PATCH", "/v1/me", beto.access, map[string]any{"name": " Beto "}); p.status != 200 || nameOf(p) != "Beto" {
		t.Fatalf("set own name: %d %s", p.status, p.raw)
	}
	if me := c.do("GET", "/v1/me", beto.access, nil); nameOf(me) != "Beto" {
		t.Fatalf("me after setting: %s", me.raw)
	}

	// Refused: blank, control characters, too long, someone else's.
	for name, body := range map[string]map[string]any{
		"blank":      {"name": "   "},
		"newline":    {"name": "Be\nto"},
		"nul":        {"name": "Be\x00to"},
		"too long":   {"name": strings.Repeat("a", 121)},
		"other user": {"name": "X", "userId": ana.tenant},
	} {
		if p := c.do("PATCH", "/v1/me", beto.access, body); p.status != 400 {
			t.Errorf("%s: %d %s", name, p.status, p.raw)
		}
	}
	for _, s := range []string{"   ", "\u200b", "\u202eoteB", "Be\u2028to"} {
		if p := c.do("PATCH", "/v1/me", beto.access, map[string]any{"name": s}); p.status != 400 || p.str("code") != "invalid_name" {
			t.Errorf("name %q: %d %s", s, p.status, p.raw)
		}
	}
	if me := c.do("GET", "/v1/me", beto.access, nil); nameOf(me) != "Beto" {
		t.Fatalf("a refused change changed the name: %s", me.raw)
	}

	// Members of the tenant see the names: the owner, and the accountant.
	acct := c.grant(deps, ana.tenant, "molino-nom-ac", "contador@nom.test", "accountant")
	for who, token := range map[string]string{"owner": ana.access, "accountant": acct.access} {
		m := c.do("GET", "/v1/tenant/members", token, nil)
		if !strings.Contains(m.raw, `"name":"Beto"`) || !strings.Contains(m.raw, `"name":"Ana Pérez"`) {
			t.Errorf("members as %s: %s", who, m.raw)
		}
	}
	// Another tenant does not see them.
	otro := c.registerAndLogin("molino-nom-c", "otro@nom.test")
	if m := c.do("GET", "/v1/tenant/members", otro.access, nil); strings.Contains(m.raw, "Beto") {
		t.Fatalf("another tenant sees the names: %s", m.raw)
	}

	// Audited, without the value.
	var n int
	var leaked bool
	if err := deps.DB.Pool.QueryRow(context.Background(), `
		select count(*), coalesce(bool_or(coalesce(detail::text, '') like '%Beto%'), false)
		from audit_log where action = 'identity.name_changed'`).Scan(&n, &leaked); err != nil || n != 1 || leaked {
		t.Fatalf("audit: %d rows, value leaked %v, %v", n, leaked, err)
	}
}

// The invitation never overwrites a name the person already set: she has
// her own account and name, is invited, and accepts giving another name.
func TestInviteDoesNotOverwriteAName(t *testing.T) {
	c := client{t, server(t)}
	own := c.registerAndLogin("molino-nom-keep", "owner@keep.test")
	mia := c.registerAndLogin("mia-nom-keep", "mia@keep.test")
	if p := c.do("PATCH", "/v1/me", mia.access, map[string]any{"name": "Mía"}); p.status != 200 {
		t.Fatalf("set name: %d %s", p.status, p.raw)
	}
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "mia@keep.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	a := c.join("mia@keep.test", map[string]any{"password": "a long enough password", "name": "Otra"})
	if a.status != 200 {
		t.Fatalf("accept: %d %s", a.status, a.raw)
	}
	if me := c.do("GET", "/v1/me", a.str("accessToken"), nil); nameOf(me) != "Mía" {
		t.Fatalf("the invitation overwrote her name: %s", me.raw)
	}
}

// An invited person can give their name when they accept.
func TestInvitedPersonGivesTheirName(t *testing.T) {
	c, _, tokens := inviteServer(t)
	own := c.registerAndLogin("molino-nom-inv", "owner@nominv.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@nominv.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := tokens()["nueva@nominv.test"][0]
	a := c.do("POST", "/v1/auth/invite/accept", "", map[string]any{"token": tok, "password": "a long enough password", "name": "Nueva Persona", "tokenDelivery": "body"})
	if a.status != 200 {
		t.Fatalf("accept: %d %s", a.status, a.raw)
	}
	if me := c.do("GET", "/v1/me", a.str("accessToken"), nil); nameOf(me) != "Nueva Persona" {
		t.Fatalf("me after accepting: %s", me.raw)
	}
}
