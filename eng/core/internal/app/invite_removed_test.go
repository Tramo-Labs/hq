package app_test

import (
	"context"
	"os"
	"strings"
	"testing"
)

// F29: someone invited and then removed before getting in. Opening the old
// link changes nothing (no password is saved), and re-adding the person
// sends a new invitation that works.
func TestRemovedInviteeCannotSetAPassword(t *testing.T) {
	deps := newDeps(t)
	_, outbox := withMagicLinks(t, deps) // mail to an outbox; invitations go there too
	c := client{t, serve(t, deps)}
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
	own := c.registerAndLogin("molino-f29", "owner@f29.test")
	hasPassword := func() bool {
		var set bool
		if err := deps.DB.Pool.QueryRow(context.Background(), `select password_hash is not null from users where email = 'nuevo@f29.test'`).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}
	add := func() {
		t.Helper()
		if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nuevo@f29.test", "role": "approver"}, idem()...); r.status != 201 {
			t.Fatalf("add: %d %s", r.status, r.raw)
		}
	}
	add()
	members := c.do("GET", "/v1/tenant/members", own.access, nil)
	var user string
	for _, m := range members.body["items"].([]any) {
		if mm := m.(map[string]any); mm["email"] == "nuevo@f29.test" {
			user = mm["userId"].(string)
		}
	}
	if r := c.do("DELETE", "/v1/tenant/members/"+user, own.access, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}

	old := tokens()["nuevo@f29.test"]
	if len(old) != 1 {
		t.Fatalf("mailed before removal: %v", old)
	}
	if r := accept(c, old[0], "a password set too early"); r.status == 200 {
		t.Fatalf("the removed person got in: %s", r.raw)
	}
	if hasPassword() {
		t.Fatal("the refused acceptance saved the password")
	}

	// Added again: a new invitation, and it works.
	add()
	toks := tokens()["nuevo@f29.test"]
	if len(toks) != 2 || toks[1] == toks[0] {
		t.Fatalf("no new invitation on re-adding: %v", toks)
	}
	if r := accept(c, toks[1], "the right password now"); r.status != 200 || r.str("role") != "approver" {
		t.Fatalf("new invitation: %d %s", r.status, r.raw)
	}
	if r := accept(c, toks[0], "the right password now"); r.status != 401 {
		t.Fatalf("the old invitation still works: %d %s", r.status, r.raw)
	}
}
