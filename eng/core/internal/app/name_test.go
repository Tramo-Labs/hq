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
	for _, s := range []string{"   ", "​", "‮oteB", "Be to"} {
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

// The invitation never overwrites a name the person already set: added
// without a password, she gets in by magic link, sets her name, then
// accepts the invitation with another one and keeps hers.
func TestInviteDoesNotOverwriteAName(t *testing.T) {
	c, tokens := magicServer(t, nil)
	own := c.registerAndLogin("molino-nom-keep", "owner@keep.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "mia@keep.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	if r := c.askLink("198.51.100.1", "mia@keep.test"); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	toks := tokens()["mia@keep.test"] // the invitation, then the link
	if len(toks) != 2 {
		t.Fatalf("mailed: %v", toks)
	}
	s := c.acceptLink("198.51.100.1", toks[1])
	if s.status != 200 {
		t.Fatalf("magic link: %d %s", s.status, s.raw)
	}
	if p := c.do("PATCH", "/v1/me", s.str("accessToken"), map[string]any{"name": "Mía"}); p.status != 200 {
		t.Fatalf("set name: %d %s", p.status, p.raw)
	}
	a := c.do("POST", "/v1/auth/invite/accept", "", map[string]any{"token": toks[0], "password": "a long enough password", "name": "Otra", "tokenDelivery": "body"})
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
