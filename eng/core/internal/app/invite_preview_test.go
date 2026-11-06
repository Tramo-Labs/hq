package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/ratelimit"
)

func preview(c client, token string) resp {
	return c.do("POST", "/v1/auth/invite/preview", "", map[string]any{"token": token})
}

// F38: the web shows the company and the role before the person accepts.
// The preview answers those two and nothing else, and uses nothing: the
// same link accepts afterwards.
func TestPreviewShowsCompanyAndRoleWithoutUsingTheInvitation(t *testing.T) {
	c := client{t, server(t)}
	own := c.registerAndLogin("molino-prev", "owner@prev.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@prev.test", "role": "approver"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := c.invitesTo("nueva@prev.test")[0]
	for i := 0; i < 2; i++ {
		r := preview(c, tok)
		if r.status != 200 || r.str("tenantName") != "Molino molino-prev" || r.str("role") != "approver" || len(r.body) != 2 {
			t.Fatalf("preview %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := accept(c, tok, "a long enough password"); r.status != 200 || r.str("role") != "approver" {
		t.Fatalf("accept after previewing: %d %s", r.status, r.raw)
	}
	if r := preview(c, tok); r.status != 401 || r.str("code") != "invalid_invite" {
		t.Fatalf("preview of a used invitation: %d %s", r.status, r.raw)
	}
}

// The preview answers like accept: 401 invalid_invite for an unknown,
// used, revoked or expired token, 403 not_a_member for a removed pending
// membership, each audited as a rejection marked via preview.
func TestPreviewRejectsLikeAccept(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-prevr", "owner@prevr.test")
	add := func(email string) string {
		t.Helper()
		r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": email, "role": "viewer"}, idem()...)
		if r.status != 201 {
			t.Fatalf("add %s: %d %s", email, r.status, r.raw)
		}
		return r.str("userId")
	}
	exec := func(sql, email string) {
		t.Helper()
		if _, err := deps.DB.Pool.Exec(context.Background(), sql, email); err != nil {
			t.Fatal(err)
		}
	}
	byUser := ` where user_id = (select id from users where email = $1)`
	add("usada@prevr.test")
	exec(`update invites set used_at = now()`+byUser, "usada@prevr.test")
	add("revocada@prevr.test")
	exec(`update invites set revoked_at = now()`+byUser, "revocada@prevr.test")
	add("vencida@prevr.test")
	exec(`update invites set expires_at = now() - interval '1 minute'`+byUser, "vencida@prevr.test")
	removed := add("sacada@prevr.test")
	if r := c.do("DELETE", "/v1/tenant/members/"+removed, own.access, nil); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.raw)
	}
	unknown := preview(c, strings.Repeat("A", 43))
	cases := map[string]struct {
		r      resp
		status int
		code   string
	}{
		"unknown": {unknown, 401, "invalid_invite"},
		"used":    {preview(c, c.invitesTo("usada@prevr.test")[0]), 401, "invalid_invite"},
		"revoked": {preview(c, c.invitesTo("revocada@prevr.test")[0]), 401, "invalid_invite"},
		"expired": {preview(c, c.invitesTo("vencida@prevr.test")[0]), 401, "invalid_invite"},
		"removed": {preview(c, c.invitesTo("sacada@prevr.test")[0]), 403, "not_a_member"},
	}
	for name, tc := range cases {
		if tc.r.status != tc.status || tc.r.str("code") != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, tc.r.status, tc.r.raw, tc.status, tc.code)
		}
		if tc.status == 401 && tc.r.str("detail") != unknown.str("detail") {
			t.Errorf("%s tells itself apart from an unknown token: %s", name, tc.r.raw)
		}
	}
	var audited int
	if err := deps.DB.Pool.QueryRow(context.Background(),
		`select count(*) from audit_log where action = 'identity.invite_rejected' and detail->>'via' = 'preview'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != len(cases) {
		t.Fatalf("%d preview rejections audited, want %d", audited, len(cases))
	}
}

// The preview shares accept's per IP cap, and every preview counts.
func TestPreviewSharesTheAcceptCap(t *testing.T) {
	deps := newDeps(t)
	deps.Identity.Limits.InvitePerIP = ratelimit.Rule{Limit: 3, Window: time.Hour}
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-prevc", "owner@prevc.test")
	if r := c.do("POST", "/v1/tenant/members", own.access, map[string]any{"email": "nueva@prevc.test", "role": "viewer"}, idem()...); r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.raw)
	}
	tok := c.invitesTo("nueva@prevc.test")[0]
	for i := 0; i < 3; i++ {
		if r := preview(c, tok); r.status != 200 {
			t.Fatalf("preview %d: %d %s", i, r.status, r.raw)
		}
	}
	if r := preview(c, tok); r.status != 429 {
		t.Fatalf("preview past the cap: %d %s", r.status, r.raw)
	}
	if r := accept(c, tok, "a long enough password"); r.status != 429 {
		t.Fatalf("accept after previews spent the cap: %d %s", r.status, r.raw)
	}
}
