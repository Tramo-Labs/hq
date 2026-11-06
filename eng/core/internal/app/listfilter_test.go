package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// The three lists a close summary links to filter by from and to (both
// included), by the same date period filters by; the pairing rules the
// spec cannot express are 400 on each route.
func TestListsFilterByDateRange(t *testing.T) {
	deps := newDeps(t)
	c := client{t: t, srv: serve(t, deps)}
	own := c.registerAndLogin("rango-datos", "owner@rango.test")
	tenant := uuid.MustParse(own.tenant)

	acct := c.do("POST", "/v1/ledger/accounts", own.access, map[string]any{"bank": "bancor", "name": "Cuenta corriente"}, idem()...)
	if acct.status != 201 {
		t.Fatalf("create account: %d %s", acct.status, acct.raw)
	}
	// Ten movements from Oct 1 to Oct 10.
	up := c.upload("/v1/ledger/accounts/"+acct.str("id")+"/statements", own.access, "oct.csv",
		ledgerFixture(t, "bancor_ok.csv"), uuid.NewString())
	if up.status != 201 || up.str("status") != "accepted" {
		t.Fatalf("upload: %d %s", up.status, up.raw)
	}
	// Two items the engine matches on its own: the transfer of Oct 1 and
	// the echeq of Oct 9.
	scope := db.WithScope(context.Background(), db.Scope{TenantID: tenant})
	oct := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	if _, err := (&matching.PGStore{DB: deps.DB}).UpsertItems(scope, "erp", []matching.ItemIn{
		{SourceID: "CH-12345", Kind: "payment", Date: oct(1), Amount: 31025075, Refs: []string{"CH 00012345"}, Confidence: 1},
		{SourceID: "FC-A-1", Kind: "invoice", Date: oct(1), Amount: 25000000, Confidence: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if r := c.do("POST", "/v1/matching/runs", own.access, map[string]any{"period": "2026-10"}, idem()...); r.status != 201 {
		t.Fatalf("run: %d %s", r.status, r.raw)
	}
	// Invoices stored on Oct 3 and Oct 20, Argentina time.
	if err := deps.DB.InTenant(scope, func(tx pgx.Tx) error {
		for i, at := range []string{"2026-10-03 12:00-03", "2026-10-20 12:00-03"} {
			if _, err := tx.Exec(context.Background(), `insert into documents.invoices
				(tenant_id, sha256, fields, trail, auto_eligible, min_confidence, created_at)
				values ($1, $2, '{}', '[]', false, 0, $3)`, tenant, []byte{byte(i)}, at); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	with := func(base, q string) string {
		if q == "" {
			return base
		}
		if strings.Contains(base, "?") {
			return base + "&" + q
		}
		return base + "?" + q
	}
	count := func(path string) int {
		t.Helper()
		r := c.do("GET", path, own.access, nil)
		items, ok := r.body["items"].([]any)
		if r.status != 200 || !ok {
			t.Fatalf("%s: %d %s", path, r.status, r.raw)
		}
		return len(items)
	}
	routes := []struct {
		base                  string
		all, early, late, oct int // no filter, Oct 1-5, Oct 6-31, period 2026-10
	}{
		// Eight movements left without candidate: three from the 1st to
		// the 5th (the transfer of the 1st is matched), five after.
		{"/v1/matching/pending?tray=exception", 8, 3, 5, 8},
		{"/v1/matching/matches?tray=auto", 2, 1, 1, 2},
		{"/v1/documents/invoices", 2, 1, 1, 2},
	}
	for _, rt := range routes {
		t.Run(rt.base, func(t *testing.T) {
			if n := count(rt.base); n != rt.all {
				t.Errorf("no filter: %d; want %d", n, rt.all)
			}
			if n := count(with(rt.base, "from=2026-10-01&to=2026-10-05")); n != rt.early {
				t.Errorf("Oct 1 to 5: %d; want %d", n, rt.early)
			}
			if n := count(with(rt.base, "from=2026-10-06&to=2026-10-31")); n != rt.late {
				t.Errorf("Oct 6 to 31: %d; want %d", n, rt.late)
			}
			if n := count(with(rt.base, "period=2026-10")); n != rt.oct {
				t.Errorf("period: %d; want %d", n, rt.oct)
			}
			if n := count(with(rt.base, "from=2026-11-01&to=2026-11-15")); n != 0 {
				t.Errorf("a fortnight with nothing: %d", n)
			}
			for name, q := range map[string]string{
				"from without to":  "from=2026-10-01",
				"to without from":  "to=2026-10-05",
				"range and period": "period=2026-10&from=2026-10-01&to=2026-10-05",
				"to before from":   "from=2026-10-05&to=2026-10-01",
			} {
				if r := c.do("GET", with(rt.base, q), own.access, nil); r.status != 400 {
					t.Errorf("%s: %d %s; want 400", name, r.status, r.raw)
				}
			}
		})
	}
}
