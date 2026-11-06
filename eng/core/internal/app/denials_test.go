package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// A member hammering a forbidden operation does not grow the audit log by
// one row per attempt: the first 403 per user and operation in a window is
// the audit row, as before; the repeats in that window add to a counter on
// that row. Nothing is lost (the count is durable), and the export shows it.
func TestRepeatedDenialsAreCoalesced(t *testing.T) {
	deps := newDeps(t)
	deps.DenialWindow = time.Second
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-rep", "owner@rep.test")
	viewer := c.member(own, own.tenant, "viewer-rep", "viewer@rep.test", "viewer")
	other := c.member(own, own.tenant, "viewer-rep2", "otro@rep.test", "viewer")

	createAccount := func(token string) int {
		return c.do("POST", "/v1/ledger/accounts", token, map[string]any{"bank": "galicia", "name": "CC"}, idem()...).status
	}
	listTenants := func(token string) int { return c.do("GET", "/v1/tenants", token, nil).status }
	for i := 0; i < 10; i++ {
		if s := createAccount(viewer.access); s != 403 {
			t.Fatalf("attempt %d: %d", i, s)
		}
	}
	for i := 0; i < 3; i++ {
		listTenants(viewer.access)
	}
	createAccount(other.access) // another person: a row of their own

	type denial struct {
		subject, email string
		repeats        int
	}
	read := func() []denial {
		var out []denial
		rs, err := deps.DB.Pool.Query(context.Background(), `
			select a.subject, u.email, coalesce(r.count, 0)
			from audit_log a join users u on u.id = a.user_id
			left join audit_denial_repeats r on r.audit_id = a.id
			where a.tenant_id = $1 and a.action = 'authz.denied' order by a.id`, own.tenant)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		for rs.Next() {
			var d denial
			if err := rs.Scan(&d.subject, &d.email, &d.repeats); err != nil {
				t.Fatal(err)
			}
			out = append(out, d)
		}
		return out
	}
	want := []denial{
		{"operation:createAccount", "viewer@rep.test", 9},
		{"operation:listTenants", "viewer@rep.test", 2},
		{"operation:createAccount", "otro@rep.test", 0},
	}
	got := read()
	if len(got) != len(want) {
		t.Fatalf("denial rows: %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, got[i], want[i])
		}
	}

	// The export carries the count.
	_, _, csvBody := c.get("/v1/exports/audit", own.access)
	if !strings.Contains(csvBody, "repeated") || !strings.Contains(csvBody, "operation:createAccount") {
		t.Fatalf("export: %s", csvBody)
	}
	var nine bool
	for _, line := range strings.Split(csvBody, "\n") {
		if strings.Contains(line, "authz.denied") && strings.Contains(line, ",9,") {
			nine = true
		}
	}
	if !nine {
		t.Errorf("export does not show the 9 repeats: %s", csvBody)
	}

	// After the window, a new row.
	time.Sleep(1100 * time.Millisecond)
	createAccount(viewer.access)
	if n := len(read()); n != 4 {
		t.Fatalf("after the window: %d rows, want 4", n)
	}

	// Under RLS another tenant sees none of these counters.
	b := c.registerAndLogin("molino-rep-b", "owner@repb.test")
	var seen int
	bctx := db.WithScope(context.Background(), db.Scope{TenantID: mustUUID(t, b.tenant)})
	if err := deps.DB.InTenant(bctx, func(tx pgx.Tx) error {
		return tx.QueryRow(bctx, `select count(*) from audit_denial_repeats`).Scan(&seen)
	}); err != nil || seen != 0 {
		t.Fatalf("tenant b sees %d counters of tenant a (%v)", seen, err)
	}
}
