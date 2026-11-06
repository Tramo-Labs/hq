package app_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var linkInMail = regexp.MustCompile(`(?m)^To: (\S+)$[\s\S]*?/ingresar#token=([A-Za-z0-9_-]+)`)

// F36 (qa/ESCENARIOS.md): someone whose only way in is a pending
// invitation, asking for a sign in link, gets the invitation again (a new
// token, the old one revoked), not a link that answers 403. The same for
// an account that asks for a link into a company where it is pending. The
// public answer is the same 202 as for an unknown email.
func TestAPendingPersonAskingForALinkGetsTheInvitation(t *testing.T) {
	deps := newDeps(t)
	deps.TrustedProxies = loopback
	q, outbox := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	links := func(email string) int {
		q.run(t)
		b, err := os.ReadFile(outbox) // #nosec G304 -- this test's outbox
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		n := 0
		for _, msg := range strings.Split(string(b), "From tramo ") {
			if m := linkInMail.FindStringSubmatch(msg); m != nil && m[1] == email {
				n++
			}
		}
		return n
	}
	ask := func(ip, email, tenant string) {
		t.Helper()
		body := map[string]any{"email": email}
		if tenant != "" {
			body["tenantId"] = tenant
		}
		r := c.do("POST", "/v1/auth/magic-link", "", body, append(idem(), "X-Forwarded-For", ip)...)
		if r.status != 202 {
			t.Fatalf("ask link for %s: %d %s", email, r.status, r.raw)
		}
	}

	// A new person invited by alfa lost the mail.
	alfa := c.registerAndLogin("alfa-f36", "owner@alfa-f36.test")
	if r := c.do("POST", "/v1/tenant/members", alfa.access, map[string]any{"email": "perdida@f36.test", "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	ask("198.51.100.61", "perdida@f36.test", "")
	if n := links("perdida@f36.test"); n != 0 {
		t.Fatalf("a pending person got %d sign in links", n)
	}
	toks := c.invitesTo("perdida@f36.test")
	if len(toks) != 2 {
		t.Fatalf("invitations mailed: %d, want the first and a fresh one", len(toks))
	}
	if r := accept(c, toks[0], "a long enough password"); r.status != 401 {
		t.Fatalf("the old invitation still works: %d %s", r.status, r.raw)
	}
	if r := accept(c, toks[1], "a long enough password"); r.status != 200 || r.str("role") != "approver" || r.str("tenantId") != alfa.tenant {
		t.Fatalf("the fresh invitation: %d %s", r.status, r.raw)
	}

	// An account active in alfa, pending in beta, asks for a link into beta.
	viewer := c.member(alfa, alfa.tenant, "viewer-f36", "viewer@f36.test", "viewer")
	beta := c.registerAndLogin("beta-f36", "owner@beta-f36.test")
	if r := c.do("POST", "/v1/tenant/members", beta.access, map[string]any{"email": viewer.email, "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("beta adds: %d %s", r.status, r.raw)
	}
	before := len(c.invitesTo(viewer.email))
	ask("198.51.100.62", viewer.email, beta.tenant)
	if n := links(viewer.email); n != 0 {
		t.Fatalf("a link into the pending company: %d", n)
	}
	if got := c.invitesTo(viewer.email); len(got) != before+1 {
		t.Fatalf("no fresh invitation to beta: %d before, %d after", before, len(got))
	}
	if r := c.join(viewer.email, nil); r.status != 200 || r.str("tenantId") != beta.tenant {
		t.Fatalf("accept beta: %d %s", r.status, r.raw)
	}

	// Without a company asked, an account with an active membership still
	// gets its link.
	ask("198.51.100.63", viewer.email, "")
	if n := links(viewer.email); n != 1 {
		t.Fatalf("sign in links for an active account: %d", n)
	}
}
