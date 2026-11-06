package app_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var inviteInMail = regexp.MustCompile(`(?m)^To: (\S+)$[\s\S]*?/invitacion#token=([A-Za-z0-9_-]+)`)

// invitesTo lists the invitation tokens this server mailed to email, in
// order.
func (c client) invitesTo(email string) []string {
	c.t.Helper()
	path, ok := outboxes.Load(c.srv.URL)
	if !ok {
		c.t.Fatal("this server has no outbox")
	}
	b, err := os.ReadFile(path.(string))
	if err != nil && !os.IsNotExist(err) {
		c.t.Fatal(err)
	}
	var out []string
	for _, msg := range strings.Split(string(b), "From tramo ") {
		if m := inviteInMail.FindStringSubmatch(msg); m != nil && m[1] == email {
			out = append(out, m[2])
		}
	}
	return out
}

// join accepts the latest invitation mailed to email, with body's extra
// fields.
func (c client) join(email string, body map[string]any) resp {
	c.t.Helper()
	toks := c.invitesTo(email)
	if len(toks) == 0 {
		c.t.Fatalf("no invitation mailed to %s", email)
	}
	b := map[string]any{"token": toks[len(toks)-1], "tokenDelivery": "body"}
	for k, v := range body {
		b[k] = v
	}
	return c.do("POST", "/v1/auth/invite/accept", "", b)
}

func memberStatus(r resp, email string) string {
	items, _ := r.body["items"].([]any)
	for _, it := range items {
		if m := it.(map[string]any); m["email"] == email {
			s, _ := m["status"].(string)
			return s
		}
	}
	return ""
}

func tenantsOf(r resp) string {
	var names []string
	ms, _ := r.body["memberships"].([]any)
	for _, m := range ms {
		names = append(names, m.(map[string]any)["tenantName"].(string))
	}
	return strings.Join(names, ",")
}

// F26, D-19 (qa/ESCENARIOS.md): adding someone who already has an account
// is an invitation. Until they accept, beta gives them nothing: no login,
// no switcher entry, and their account is not written.
func TestAddingAnExistingAccountIsAnInvitation(t *testing.T) {
	c := client{t, server(t)}
	alfa := c.registerAndLogin("alfa-f26", "owner@alfa-f26.test")
	viewer := c.member(alfa, alfa.tenant, "viewer-f26", "viewer@alfa-f26.test", "viewer")
	beta := c.registerAndLogin("beta-f26", "owner@beta-f26.test")
	before := len(c.invitesTo(viewer.email))

	add := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": viewer.email, "role": "approver"}, idem()...)
	if add.status != 201 || add.str("status") != "pending" || add.body["name"] != nil {
		t.Fatalf("add: %d %s", add.status, add.raw)
	}
	if got := c.invitesTo(viewer.email); len(got) != before+1 {
		t.Fatalf("no invitation mailed: %d before, %d after", before, len(got))
	}
	if s := memberStatus(c.do("GET", "/v1/tenant/members", beta.access, nil), viewer.email); s != "pending" {
		t.Fatalf("beta lists the person as %q", s)
	}
	if me := c.do("GET", "/v1/me", viewer.access, nil); strings.Contains(tenantsOf(me), "beta-f26") {
		t.Fatalf("a pending membership shows in the switcher: %s", me.raw)
	}
	login := func(password string) resp {
		return c.do("POST", "/v1/auth/login", "", map[string]any{"email": viewer.email, "password": password, "tenantId": beta.tenant, "tokenDelivery": "body"})
	}
	if r := login(viewer.password); r.status != 403 || r.str("code") != "not_a_member" {
		t.Fatalf("login into the pending tenant: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": viewer.email, "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("adding someone pending again: %d %s", r.status, r.raw)
	}
	if got := c.invitesTo(viewer.email); len(got) != before+2 {
		t.Fatalf("adding again sent no fresh invitation: %d", len(got)-before)
	}

	// Accepting: a password given is ignored, the old one keeps working.
	acc := c.join(viewer.email, map[string]any{"password": "an attempt to change it"})
	if acc.status != 200 || acc.str("role") != "approver" {
		t.Fatalf("accept: %d %s", acc.status, acc.raw)
	}
	if r := login("an attempt to change it"); r.status != 401 {
		t.Fatalf("accepting changed the password: %d", r.status)
	}
	if r := login(viewer.password); r.status != 200 || r.str("role") != "approver" {
		t.Fatalf("login after accepting: %d %s", r.status, r.raw)
	}
	if s := memberStatus(c.do("GET", "/v1/tenant/members", beta.access, nil), viewer.email); s != "active" {
		t.Fatalf("after accepting beta lists the person as %q", s)
	}
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": viewer.email, "role": "viewer"}, idem()...); r.status != 409 {
		t.Fatalf("adding an active member: %d %s", r.status, r.raw)
	}
}

// F26: a new person sets a password to accept, and a refusal for lack of
// one leaves the invitation usable.
func TestANewPersonSetsAPasswordToAccept(t *testing.T) {
	c := client{t, server(t)}
	own := c.registerAndLogin("molino-f26n", "owner@f26n.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@f26n.test", "role": "viewer"}, idem()...); r.status != 201 || r.str("status") != "pending" {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	if r := c.join("nueva@f26n.test", nil); r.status != 400 || r.str("code") != "password_required" {
		t.Fatalf("accept without a password: %d %s", r.status, r.raw)
	}
	if r := c.join("nueva@f26n.test", map[string]any{"password": "a long enough password"}); r.status != 200 || r.str("role") != "viewer" {
		t.Fatalf("accept: %d %s", r.status, r.raw)
	}
}

// F26: removing someone pending takes away what the invitation would give.
func TestRemovingAPendingMember(t *testing.T) {
	c := client{t, server(t)}
	own := c.registerAndLogin("molino-f26r", "owner@f26r.test")
	add := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "quita@f26r.test", "role": "viewer"}, idem()...)
	if r := c.do("DELETE", "/v1/tenant/members/"+add.str("userId"), own.access, nil); r.status != 204 {
		t.Fatalf("remove pending: %d %s", r.status, r.raw)
	}
	if s := memberStatus(c.do("GET", "/v1/tenant/members", own.access, nil), "quita@f26r.test"); s != "" {
		t.Fatalf("still listed as %q", s)
	}
	if r := c.join("quita@f26r.test", map[string]any{"password": "a long enough password"}); r.status != 403 || r.str("code") != "not_a_member" {
		t.Fatalf("accept after removal: %d %s", r.status, r.raw)
	}
}
