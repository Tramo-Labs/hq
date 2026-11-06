package app_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
)

var tokenInMail = regexp.MustCompile(`(?m)^To: (\S+)$[\s\S]*?#token=([A-Za-z0-9_-]+)`)

// inviteServer is a server whose invitations land in an outbox file.
func inviteServer(t *testing.T) (client, string, func() map[string][]string) {
	t.Helper()
	deps := newDeps(t)
	outbox := filepath.Join(t.TempDir(), "outbox")
	deps.Identity.Mailer, deps.Identity.AppBaseURL = &mail.Outbox{Path: outbox}, "https://app.example.test"
	c := client{t, serve(t, deps)}
	// tokens lists the invitation tokens mailed to each address, in order.
	tokens := func() map[string][]string {
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
	return c, outbox, tokens
}

func accept(c client, token, password string) resp {
	return c.do("POST", "/v1/auth/invite/accept", "", map[string]any{"token": token, "password": password, "tokenDelivery": "body"})
}

// F8 (cliente, qa/ESCENARIOS.md): a person added without an account gets
// in through a one time invitation, mailed and never returned by the API.
func TestInvitationLetsANewPersonIn(t *testing.T) {
	c, outbox, tokens := inviteServer(t)
	own := c.registerAndLogin("molino-f8", "owner@f8.test")
	add := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@f8.test", "role": "approver"}, idem()...)
	if add.status != 201 {
		t.Fatalf("add: %d %s", add.status, add.raw)
	}
	toks := tokens()["nueva@f8.test"]
	if len(toks) != 1 || len(toks[0]) != 43 {
		t.Fatalf("mailed tokens: %v", tokens())
	}
	tok := toks[0]
	if strings.Contains(add.raw, tok) {
		t.Fatal("the add member response carries the token")
	}
	if st, err := os.Stat(outbox); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("outbox mode: %v %v", st.Mode().Perm(), err)
	}
	b, _ := os.ReadFile(outbox)
	if !strings.Contains(string(b), "https://app.example.test/invitacion#token="+tok) {
		t.Fatalf("link not in the fragment:\n%s", b)
	}
	// Before accepting, no password works.
	if r := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "nueva@f8.test", "password": "a long enough password"}); r.status != 401 {
		t.Fatalf("login before accepting: %d", r.status)
	}
	// Short password: refused like any bad request, the token survives.
	if r := accept(c, tok, "short"); r.status != 400 {
		t.Fatalf("short password: %d %s", r.status, r.raw)
	}
	r := accept(c, tok, "a long enough password")
	if r.status != 200 || r.str("role") != "approver" || r.str("tenantId") != own.tenant || r.str("accessToken") == "" {
		t.Fatalf("accept: %d %s", r.status, r.raw)
	}
	// Single use.
	if again := accept(c, tok, "another long password"); again.status != 401 || again.str("code") != "invalid_invite" {
		t.Fatalf("second use: %d %s", again.status, again.raw)
	}
	l := c.do("POST", "/v1/auth/login", "", map[string]any{"email": "nueva@f8.test", "password": "a long enough password", "tokenDelivery": "body"})
	if l.status != 200 || l.str("tenantId") != own.tenant {
		t.Fatalf("login after accepting: %d %s", l.status, l.raw)
	}
	// Someone who already has a password gets an invitation too (D-19).
	c.registerAndLogin("otra-f8", "existente@f8.test")
	c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "existente@f8.test", "role": "viewer"}, idem()...)
	if got := tokens()["existente@f8.test"]; len(got) != 1 {
		t.Fatalf("invitations for a person with a password: %v", got)
	}
	// The audit log has the invitation and its use, never the token.
	_, _, audit := c.get("/v1/exports/audit", own.access)
	if !strings.Contains(audit, "identity.invite_created") || !strings.Contains(audit, "identity.invite_accepted") || strings.Contains(audit, tok) {
		t.Fatalf("audit:\n%s", audit)
	}
}

// Unknown, used, expired and superseded invitations all answer the same.
func TestInvitationsThatCanNotBeUsed(t *testing.T) {
	deps := newDeps(t)
	outbox := filepath.Join(t.TempDir(), "outbox")
	deps.Identity.Mailer = &mail.Outbox{Path: outbox}
	c := client{t, serve(t, deps)}
	tokens := func(email string) []string {
		b, _ := os.ReadFile(outbox)
		var out []string
		for _, msg := range strings.Split(string(b), "From tramo ") {
			if m := tokenInMail.FindStringSubmatch(msg); m != nil && m[1] == email {
				out = append(out, m[2])
			}
		}
		return out
	}
	a := c.registerAndLogin("molino-f8a", "owner@f8a.test")
	b := c.registerAndLogin("molino-f8b", "owner@f8b.test")

	// Expired.
	c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "tarde@f8.test", "role": "viewer"}, idem()...)
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`update invites set expires_at = now() - interval '1 minute' where user_id = (select id from users where email = 'tarde@f8.test')`); err != nil {
		t.Fatal(err)
	}
	expired := accept(c, tokens("tarde@f8.test")[0], "a long enough password")

	// Superseded: the same tenant invites the person again; its first link
	// dies. Another tenant's invitation stays valid.
	c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "doble@f8.test", "role": "viewer"}, idem()...)
	c.do("POST", "/v1/tenant/members", b.access, map[string]any{"email": "doble@f8.test", "role": "approver"}, idem()...)
	c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "doble@f8.test", "role": "viewer"}, idem()...)
	doble := tokens("doble@f8.test")
	if len(doble) != 3 {
		t.Fatalf("tokens for doble: %v", doble)
	}
	superseded := accept(c, doble[0], "a long enough password")

	// Used, even while the person still has no password (the used mark is
	// its own guard, not only the password one).
	c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "usada@f8.test", "role": "viewer"}, idem()...)
	if _, err := deps.DB.Pool.Exec(context.Background(),
		`update invites set used_at = now() where user_id = (select id from users where email = 'usada@f8.test')`); err != nil {
		t.Fatal(err)
	}
	used := accept(c, tokens("usada@f8.test")[0], "a long enough password")
	unknown := accept(c, strings.Repeat("A", 43), "a long enough password")
	for name, r := range map[string]resp{"expired": expired, "superseded": superseded, "used": used, "unknown": unknown} {
		if r.status != 401 || r.str("code") != "invalid_invite" || r.str("detail") != unknown.str("detail") {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	// The other tenant's link and the newest one both work, each opening
	// the tenant that sent it.
	if r := accept(c, doble[1], "a long enough password"); r.status != 200 || r.str("tenantId") != b.tenant {
		t.Fatalf("other tenant's invitation: %d %s", r.status, r.raw)
	}
	if r := accept(c, doble[2], "a long enough password"); r.status != 200 || r.str("tenantId") != a.tenant {
		t.Fatalf("newest invitation: %d %s", r.status, r.raw)
	}

	// Two tabs accepting the same link at once: one session, one password.
	c.do("POST", "/v1/tenant/members", a.access, map[string]any{"email": "rapida@f8.test", "role": "viewer"}, idem()...)
	tok := tokens("rapida@f8.test")[0]
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, pw := range []string{"first long password", "second long password"} {
		wg.Add(1)
		go func() { defer wg.Done(); statuses[i] = accept(c, tok, pw).status }()
	}
	wg.Wait()
	if statuses[0]+statuses[1] != 200+401 {
		t.Fatalf("concurrent accepts: %v", statuses)
	}

	// Rate limited per IP: 10 per 15 minutes. Used so far: 7.
	var last resp
	for i := 0; i < 4; i++ {
		last = accept(c, strings.Repeat("B", 43), "a long enough password")
	}
	if last.status != 429 || last.header.Get("Retry-After") == "" {
		t.Fatalf("11th accept from one IP: %d %s", last.status, last.raw)
	}

	// Only hashes are stored.
	var n int
	if err := deps.DB.Pool.QueryRow(context.Background(), `select count(*) from invites where token_hash = convert_to($1, 'UTF8')`, tok).Scan(&n); err != nil || n != 0 {
		t.Fatalf("token stored in clear: %d %v", n, err)
	}
}
