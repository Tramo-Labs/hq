package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// pendingNumbers is a stand in for matching's period numbers (datos).
type pendingNumbers struct{ pending int }

func (p *pendingNumbers) PendingSuggested(context.Context, pgx.Tx, time.Time, time.Time) (int, error) {
	return p.pending, nil
}

func (p *pendingNumbers) Summary(context.Context, pgx.Tx, time.Time, time.Time, time.Time) ([]closes.Metric, error) {
	v := 72.0
	return []closes.Metric{{Key: "automation_rate", Label: "Automático", Unit: "percent", Value: &v, Source: "s", List: "/v1/x"},
		{Key: "open_exceptions", Label: "Excepciones", Unit: "count", Source: "s", List: "/v1/y"}}, nil
}

// 17 Nov 2026 at noon in Argentina: 1 to 15 Nov is over.
var closeClock = func() time.Time { return time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC) }

// Only an operator closes: every other role gets 403, even while the
// period numbers are not provided yet (not 501, not 201).
func TestOnlyAnOperatorCloses(t *testing.T) {
	for _, withNumbers := range []bool{false, true} {
		deps := newDeps(t)
		deps.Now = closeClock
		if withNumbers {
			deps.CloseNumbers = &pendingNumbers{}
		}
		c := client{t, serve(t, deps)}
		who := c.roles(deps, map[bool]string{false: "molino-cl0", true: "molino-cl1"}[withNumbers])
		for _, role := range []string{"owner", "approver", "viewer", "accountant"} {
			r := c.do("POST", "/v1/closes", who[role].access, map[string]any{"periodStart": "2026-11-01"}, idem()...)
			if r.status != 403 {
				t.Errorf("numbers %v: %s closing: %d %s, want 403", withNumbers, role, r.status, r.raw)
			}
		}
		want := map[bool]int{false: 501, true: 201}[withNumbers]
		if r := c.do("POST", "/v1/closes", who["operator"].access, map[string]any{"periodStart": "2026-11-01"}, idem()...); r.status != want {
			t.Errorf("numbers %v: operator closing: %d %s, want %d", withNumbers, r.status, r.raw, want)
		}
	}
}

func TestClosesOverHTTP(t *testing.T) {
	deps := newDeps(t)
	deps.Now = closeClock
	numbers := &pendingNumbers{pending: 4}
	deps.CloseNumbers = numbers
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-cl")
	op := who["operator"].access
	body := map[string]any{"periodStart": "2026-11-01"}

	if r := c.do("POST", "/v1/closes", op, body); r.status != 400 {
		t.Fatalf("close without Idempotency-Key: %d", r.status)
	}
	r := c.do("POST", "/v1/closes", op, body, idem()...)
	if r.status != 409 || r.str("code") != "suggested_pending" || r.body["pendingSuggested"] != float64(4) {
		t.Fatalf("close with Suggested pending: %d %s", r.status, r.raw)
	}
	for start, want := range map[string]string{"2026-11-02": "invalid_period", "2026-11-16": "period_not_over"} {
		if r := c.do("POST", "/v1/closes", op, map[string]any{"periodStart": start}, idem()...); r.str("code") != want {
			t.Fatalf("close %s: %d %s, want %s", start, r.status, r.raw, want)
		}
	}

	// The console shows the period to close once the job marked it.
	svc := &closes.Service{DB: deps.DB, Now: closeClock}
	period, _ := closes.PeriodStarting(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), closes.Fortnight)
	if _, err := svc.MarkDue(jobs.TenantContext(context.Background(), mustUUID(t, who["owner"].tenant)), period); err != nil {
		t.Fatal(err)
	}
	toClose := func() []any {
		r := c.do("GET", "/v1/tenants", op, nil)
		items, _ := r.body["items"].([]any)
		if r.status != 200 || len(items) != 1 {
			t.Fatalf("console: %d %s", r.status, r.raw)
		}
		tc, _ := items[0].(map[string]any)["toClose"].([]any)
		return tc
	}
	if tc := toClose(); len(tc) != 1 || tc[0].(map[string]any)["from"] != "2026-11-01" || tc[0].(map[string]any)["to"] != "2026-11-15" {
		t.Fatalf("console toClose before the close: %v", tc)
	}

	numbers.pending = 0
	key := idem()
	r = c.do("POST", "/v1/closes", op, body, key...)
	if r.status != 201 || r.str("periodStart") != "2026-11-01" || r.str("periodEnd") != "2026-11-15" || r.str("cadence") != "fortnight" {
		t.Fatalf("close: %d %s", r.status, r.raw)
	}
	id := r.str("id")
	summary, _ := r.body["summary"].([]any)
	if len(summary) != 2 || summary[0].(map[string]any)["period"] != "2026-11-01/2026-11-15" ||
		summary[0].(map[string]any)["status"] != "measured" || summary[1].(map[string]any)["status"] != "no_data" {
		t.Fatalf("summary: %v", summary)
	}
	if again := c.do("POST", "/v1/closes", op, body, key...); again.status != 201 || again.str("id") != id {
		t.Fatalf("same key replayed: %d %s", again.status, again.raw)
	}
	if r := c.do("POST", "/v1/closes", op, body, idem()...); r.status != 409 || r.str("code") != "already_closed" {
		t.Fatalf("close twice: %d %s", r.status, r.raw)
	}
	if tc := toClose(); len(tc) != 0 {
		t.Fatalf("console toClose after the close: %v", tc)
	}

	// Every role of the tenant reads it; another tenant does not.
	for role, a := range who {
		l := c.do("GET", "/v1/closes", a.access, nil)
		items, _ := l.body["items"].([]any)
		if l.status != 200 || len(items) != 1 || l.body["nextCursor"] != nil {
			t.Errorf("%s lists: %d %s", role, l.status, l.raw)
		}
		if g := c.do("GET", "/v1/closes/"+id, a.access, nil); g.status != 200 || g.str("id") != id {
			t.Errorf("%s gets: %d %s", role, g.status, g.raw)
		}
	}
	other := c.registerAndLogin("molino-cl-otro", "otro@cl.test")
	if g := c.do("GET", "/v1/closes/"+id, other.access, nil); g.status != 404 {
		t.Fatalf("another tenant gets the close: %d", g.status)
	}
	if l := c.do("GET", "/v1/closes", other.access, nil); l.status != 200 || len(l.body["items"].([]any)) != 0 {
		t.Fatalf("another tenant lists: %s", l.raw)
	}
	if l := c.do("GET", "/v1/closes?cursor=nope", op, nil); l.status != 400 {
		t.Fatalf("bad cursor: %d", l.status)
	}
}
