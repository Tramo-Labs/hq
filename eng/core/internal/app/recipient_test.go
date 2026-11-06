package app_test

import (
	"context"
	"os"
	"strings"
	"testing"
)

// F35 (F25 family): mail goes to the address stored for the account, never
// to the casing someone typed. An account stored before emails were
// lowercased shows it: the sign in link and the invitation both go to its
// stored address.
func TestMailGoesToTheStoredAddress(t *testing.T) {
	deps := newDeps(t)
	deps.TrustedProxies = loopback
	q, outbox := withMagicLinks(t, deps)
	c := client{t, serve(t, deps)}
	recipients := func() map[string]int {
		q.run(t)
		b, err := os.ReadFile(outbox) // #nosec G304 -- this test's outbox
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, msg := range strings.Split(string(b), "From tramo ") {
			if m := tokenInMail.FindStringSubmatch(msg); m != nil {
				out[m[1]]++
			}
		}
		return out
	}
	own := c.registerAndLogin("molino-f35", "owner@f35.test")
	c.registerAndLogin("otra-f35", "otra@f35.test")
	if r := c.askLink("198.51.100.35", "  OTRA@F35.Test "); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	if got := recipients(); got["otra@f35.test"] != 1 || len(got) != 1 {
		t.Fatalf("link typed in other case went to %v", got)
	}

	// The same account as stored before lowercasing.
	if _, err := deps.DB.Pool.Exec(context.Background(), `update users set email = 'Otra@F35.Test' where email = 'otra@f35.test'`); err != nil {
		t.Fatal(err)
	}
	if r := c.askLink("198.51.100.36", "otra@f35.test"); r.status != 202 {
		t.Fatalf("ask link: %d %s", r.status, r.raw)
	}
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "OTRA@f35.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	if got := recipients(); got["Otra@F35.Test"] != 2 || len(got) != 2 {
		t.Fatalf("mail to a legacy address went to %v", got)
	}
}
