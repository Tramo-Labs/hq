package app_test

import (
	"fmt"
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
	// Anyone can ask for a link with this email: the resend revokes
	// nothing, so the link the person already had keeps working. Once one
	// is used, the other is spent.
	if r := accept(c, toks[0], "a long enough password"); r.status != 200 || r.str("role") != "approver" || r.str("tenantId") != alfa.tenant {
		t.Fatalf("the invitation the person had, after a resend: %d %s", r.status, r.raw)
	}
	if r := accept(c, toks[1], "a long enough password"); r.status != 401 {
		t.Fatalf("the resent invitation after the person joined: %d %s", r.status, r.raw)
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

// One link request resends at most 5 pending invitations (and the request
// is capped at 3 per email every 15 minutes: at most 15 per person).
func TestALinkRequestResendsAtMostFive(t *testing.T) {
	deps := newDeps(t)
	deps.TrustedProxies = loopback
	q, _ := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	for i := 0; i < 7; i++ {
		own := c.registerAndLogin(fmt.Sprintf("molino-f36-%d", i), fmt.Sprintf("owner%d@f36m.test", i))
		if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "muchas@f36m.test", "role": "viewer"}, idem()...); r.status != 201 {
			t.Fatalf("add %d: %d %s", i, r.status, r.raw)
		}
	}
	before := len(c.invitesTo("muchas@f36m.test"))
	if r := c.do("POST", "/v1/auth/magic-link", "", map[string]any{"email": "muchas@f36m.test"}, append(idem(), "X-Forwarded-For", "198.51.100.64")...); r.status != 202 {
		t.Fatalf("ask: %d %s", r.status, r.raw)
	}
	q.run(t)
	if got := len(c.invitesTo("muchas@f36m.test")) - before; got != 5 {
		t.Fatalf("one request resent %d invitations, want 5", got)
	}
}
