package closes_test

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/closes"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// fakeNumbers stands in for matching (datos): a pending count and summary,
// and what it was asked.
type fakeNumbers struct {
	pending          int
	from, to         time.Time
	summary          []closes.Metric
	summaryCalls     atomic.Int32
	pendingFromQuery func(ctx context.Context, tx pgx.Tx) (int, error)
}

func (f *fakeNumbers) PendingSuggested(ctx context.Context, tx pgx.Tx, from, to time.Time) (int, error) {
	f.from, f.to = from, to
	if f.pendingFromQuery != nil {
		return f.pendingFromQuery(ctx, tx)
	}
	return f.pending, nil
}

func (f *fakeNumbers) Summary(context.Context, pgx.Tx, time.Time, time.Time, time.Time) ([]closes.Metric, error) {
	f.summaryCalls.Add(1)
	return f.summary, nil
}

type env struct {
	db                          *db.DB
	tenantA, tenantB            uuid.UUID
	operator, viewer, operatorB uuid.UUID
}

func newEnv(t *testing.T) env {
	t.Helper()
	e := env{db: dbtest.New(t), tenantA: uuid.New(), tenantB: uuid.New(),
		operator: uuid.New(), viewer: uuid.New(), operatorB: uuid.New()}
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into tenants (id, slug, name) values ($1, 'molino-a', 'A'), ($2, 'molino-b', 'B')`, []any{e.tenantA, e.tenantB}},
		{`insert into users (id, email, password_hash) values ($1, 'op@t.test', 'x'), ($2, 'vw@t.test', 'x'), ($3, 'opb@t.test', 'x')`, []any{e.operator, e.viewer, e.operatorB}},
		{`insert into memberships (user_id, tenant_id, role) values ($1, $3, 'operator'), ($2, $3, 'viewer'), ($4, $5, 'operator')`, []any{e.operator, e.viewer, e.tenantA, e.operatorB, e.tenantB}},
	} {
		if _, err := e.db.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func as(tenant, user uuid.UUID) context.Context {
	return db.WithScope(context.Background(), db.Scope{TenantID: tenant, UserID: user})
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// 17 Nov 2026 at noon in Argentina: the fortnight of 1 to 15 Nov is over,
// the one of 16 to 30 Nov is not.
var noon17Nov = time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)

func service(e env, n closes.Numbers) *closes.Service {
	s := &closes.Service{DB: e.db, Now: func() time.Time { return noon17Nov }}
	if n != nil {
		s.Numbers = n
	}
	return s
}

func count(t *testing.T, e env, sql string) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Only an operator closes, only a period that is over, starts where a
// period starts and has no Suggested pending; every refusal writes
// nothing, and the close freezes what Summary gave for exactly that range.
func TestClosePeriodRules(t *testing.T) {
	e := newEnv(t)
	n := &fakeNumbers{pending: 3}
	s := service(e, n)
	op := as(e.tenantA, e.operator)

	if _, err := s.ClosePeriod(as(e.tenantA, e.viewer), day(2026, 11, 1)); !errors.Is(err, closes.ErrForbidden) {
		t.Fatalf("viewer closed: %v", err)
	}
	if _, err := s.ClosePeriod(as(e.tenantA, e.operatorB), day(2026, 11, 1)); !errors.Is(err, closes.ErrForbidden) {
		t.Fatalf("an operator of another tenant closed: %v", err)
	}
	if _, err := s.ClosePeriod(op, day(2026, 11, 2)); !errors.Is(err, closes.ErrInvalidPeriod) {
		t.Fatalf("2 Nov taken as a period start: %v", err)
	}
	if _, err := s.ClosePeriod(op, day(2026, 11, 16)); !errors.Is(err, closes.ErrNotOver) {
		t.Fatalf("a running fortnight closed: %v", err)
	}
	var pe *closes.PendingError
	if _, err := s.ClosePeriod(op, day(2026, 11, 1)); !errors.As(err, &pe) || pe.N != 3 {
		t.Fatalf("closed with 3 Suggested pending: %v", err)
	}
	if !n.from.Equal(day(2026, 11, 1)) || !n.to.Equal(day(2026, 11, 16)) {
		t.Fatalf("pending asked for [%s, %s), want [1 Nov, 16 Nov)", n.from, n.to)
	}
	if n.summaryCalls.Load() != 0 {
		t.Fatal("summary computed for a refused close")
	}
	if got := count(t, e, `select count(*) from closes.closes`); got != 0 {
		t.Fatalf("refusals wrote %d closes", got)
	}

	v := 81.5
	n.pending, n.summary = 0, []closes.Metric{{Key: "automation_rate", Label: "Automático", Unit: "percent", Value: &v,
		Source: "movements settled by a rule", List: "/v1/matching/matches?tray=auto"}, {Key: "open_exceptions", Unit: "count"}}
	c, err := s.ClosePeriod(op, day(2026, 11, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Period.Start.Equal(day(2026, 11, 1)) || !c.Period.LastDay().Equal(day(2026, 11, 15)) || c.ClosedBy != e.operator {
		t.Fatalf("close: %+v", c)
	}
	if len(c.Summary) != 2 || c.Summary[0].Value == nil || *c.Summary[0].Value != 81.5 || c.Summary[1].Value != nil {
		t.Fatalf("summary not frozen as given: %+v", c.Summary)
	}
	if _, err := s.ClosePeriod(op, day(2026, 11, 1)); !errors.Is(err, closes.ErrAlreadyClosed) {
		t.Fatalf("closed twice: %v", err)
	}
	if got := count(t, e, `select count(*) from audit_log where action = 'closes.close'`); got != 1 {
		t.Fatalf("%d audit rows for one close", got)
	}

	got, err := s.Get(op, c.ID)
	if err != nil || got.ID != c.ID || len(got.Summary) != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.Get(as(e.tenantB, e.operatorB), c.ID); !errors.Is(err, closes.ErrNotFound) {
		t.Fatalf("tenant B reads A's close: %v", err)
	}
	if list, _, err := s.List(as(e.tenantB, e.operatorB), nil); err != nil || len(list) != 0 {
		t.Fatalf("tenant B lists %d closes: %v", len(list), err)
	}
	if list, _, err := s.List(as(e.tenantA, e.viewer), nil); err != nil || len(list) != 1 {
		t.Fatalf("viewer lists %d closes: %v", len(list), err)
	}
}

// Without matching's numbers nothing closes, but the role is checked
// first: a non operator is refused, not told it is not built.
func TestNoNumbersNoClose(t *testing.T) {
	e := newEnv(t)
	s := service(e, nil)
	if _, err := s.ClosePeriod(as(e.tenantA, e.viewer), day(2026, 11, 1)); !errors.Is(err, closes.ErrForbidden) {
		t.Fatalf("viewer without numbers: %v, want forbidden", err)
	}
	if _, err := s.ClosePeriod(as(e.tenantA, e.operator), day(2026, 11, 1)); !errors.Is(err, closes.ErrNoNumbers) {
		t.Fatalf("closed without numbers: %v", err)
	}
}

// Pages of ListPageSize, newest first; the cursor continues without
// repeating or skipping a close.
func TestListPages(t *testing.T) {
	e := newEnv(t)
	s := &closes.Service{DB: e.db, Numbers: &fakeNumbers{}, Now: func() time.Time { return day(2030, 1, 1) }}
	op := as(e.tenantA, e.operator)
	total := closes.ListPageSize + 3
	p := closes.PeriodOf(day(2027, 1, 1), closes.Fortnight)
	for i := 0; i < total; i++ {
		if _, err := s.ClosePeriod(op, p.Start); err != nil {
			t.Fatal(err)
		}
		p = closes.PeriodOf(p.End, closes.Fortnight)
	}
	first, more, err := s.List(op, nil)
	if err != nil || len(first) != closes.ListPageSize || !more {
		t.Fatalf("first page: %d more %v %v", len(first), more, err)
	}
	cursor := first[len(first)-1].Period.Start
	second, more, err := s.List(op, &cursor)
	if err != nil || len(second) != 3 || more {
		t.Fatalf("second page: %d more %v %v", len(second), more, err)
	}
	seen := map[time.Time]bool{}
	prev := day(2100, 1, 1)
	for _, c := range append(first, second...) {
		if seen[c.Period.Start] || !c.Period.Start.Before(prev) {
			t.Fatalf("page order broken at %s", c.Period.Start)
		}
		seen[c.Period.Start], prev = true, c.Period.Start
	}
}

// A date is closed when it falls in [start, end) of a close of its own
// tenant: the last day is in, the next day is not, another tenant's close
// does not count, and the period named is the earliest one hit.
func TestClosedOnAndThePeriodItNames(t *testing.T) {
	e := newEnv(t)
	s := service(e, &fakeNumbers{})
	op := as(e.tenantA, e.operator)
	for _, start := range []time.Time{day(2026, 10, 16), day(2026, 11, 1)} {
		if _, err := s.ClosePeriod(op, start); err != nil {
			t.Fatal(err)
		}
	}
	check := func(ctx context.Context, dates []time.Time, want bool, wantStart time.Time) {
		t.Helper()
		err := e.db.InTenant(ctx, func(tx pgx.Tx) error {
			closed, err := closes.ClosedOn(ctx, tx, dates)
			if err != nil {
				return err
			}
			p, ok, err := closes.ClosedPeriodOf(ctx, tx, dates)
			if err != nil {
				return err
			}
			if closed != want || ok != want || (want && !p.Start.Equal(wantStart)) {
				t.Errorf("%v: closed %v, period %v %s; want %v %s", dates, closed, ok, p.Start, want, wantStart)
			}
			var sql bool
			if err := tx.QueryRow(ctx, `select coalesce(bool_or(closes.closed_on(d)), false) from unnest($1::date[]) d`, dates).Scan(&sql); err != nil {
				return err
			}
			if sql != want {
				t.Errorf("%v: closes.closed_on says %v, want %v", dates, sql, want)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(op, []time.Time{day(2026, 11, 15)}, true, day(2026, 11, 1))
	check(op, []time.Time{day(2026, 11, 16)}, false, time.Time{})
	check(op, []time.Time{day(2026, 10, 15)}, false, time.Time{})
	check(op, []time.Time{day(2026, 10, 16)}, true, day(2026, 10, 16))
	check(op, []time.Time{day(2026, 11, 20), day(2026, 11, 3), day(2026, 10, 31)}, true, day(2026, 10, 16))
	// A date with an hour is its calendar day.
	check(op, []time.Time{time.Date(2026, 11, 15, 23, 59, 0, 0, time.UTC)}, true, day(2026, 11, 1))
	check(as(e.tenantB, e.operatorB), []time.Time{day(2026, 11, 3)}, false, time.Time{})
}

// A write that asked ClosedOn and saw the period open holds the close back
// until it commits, so what it adds is counted: the close then refuses. A
// close never counts the tray while such a write is half done.
func TestACloseWaitsForAWriteThatSawItOpen(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, q := range []string{
		`create table public.fake_suggested (tenant_id uuid not null)`,
		`alter table public.fake_suggested enable row level security`,
		`alter table public.fake_suggested force row level security`,
		`create policy t on public.fake_suggested to tramo_app using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant())`,
		`grant select, insert on public.fake_suggested to tramo_app`,
	} {
		if _, err := e.db.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	n := &fakeNumbers{pendingFromQuery: func(ctx context.Context, tx pgx.Tx) (int, error) {
		var c int
		err := tx.QueryRow(ctx, `select count(*) from public.fake_suggested`).Scan(&c)
		return c, err
	}}
	s := service(e, n)
	op := as(e.tenantA, e.operator)

	asked := make(chan struct{})
	release := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- e.db.InTenant(op, func(tx pgx.Tx) error {
			closed, err := closes.ClosedOn(op, tx, []time.Time{day(2026, 11, 10)})
			if err != nil {
				return err
			}
			if closed {
				return errors.New("saw the period closed")
			}
			close(asked)
			<-release
			_, err = tx.Exec(op, `insert into public.fake_suggested values (app_current_tenant())`)
			return err
		})
	}()
	<-asked

	closeDone := make(chan error, 1)
	go func() { _, err := s.ClosePeriod(op, day(2026, 11, 1)); closeDone <- err }()
	select {
	case err := <-closeDone:
		close(release)
		<-writeDone
		t.Fatalf("the close did not wait for the write that saw it open: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	var pe *closes.PendingError
	if err := <-closeDone; !errors.As(err, &pe) || pe.N != 1 {
		t.Fatalf("close after the write: %v, want 1 Suggested pending", err)
	}
}

// The job's mark: only a period that is over, once per tenant and period,
// and a closed period is no longer pending.
func TestMarkDue(t *testing.T) {
	e := newEnv(t)
	s := service(e, &fakeNumbers{})
	job := db.WithScope(context.Background(), db.Scope{TenantID: e.tenantA})
	first, _ := closes.PeriodStarting(day(2026, 11, 1), closes.Fortnight)
	running, _ := closes.PeriodStarting(day(2026, 11, 16), closes.Fortnight)

	if _, err := s.MarkDue(job, running); !errors.Is(err, closes.ErrNotOver) {
		t.Fatalf("marked a running fortnight: %v", err)
	}
	for i, wantNew := range []bool{true, false} {
		isNew, err := s.MarkDue(job, first)
		if err != nil || isNew != wantNew {
			t.Fatalf("mark %d: new %v %v, want %v", i, isNew, err, wantNew)
		}
	}
	due, err := s.PendingDue(as(e.tenantA, e.viewer))
	if err != nil || len(due) != 1 || !due[0].Period.Start.Equal(first.Start) {
		t.Fatalf("pending due: %+v %v", due, err)
	}
	if due, _ := s.PendingDue(as(e.tenantB, e.operatorB)); len(due) != 0 {
		t.Fatalf("tenant B sees A's mark: %+v", due)
	}
	if _, err := s.ClosePeriod(as(e.tenantA, e.operator), first.Start); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.PendingDue(as(e.tenantA, e.viewer)); len(due) != 0 {
		t.Fatalf("a closed period still pending: %+v", due)
	}
}

// The job marks the latest fortnight that is over in Argentina's time.
func TestLastOver(t *testing.T) {
	for at, want := range map[time.Time]time.Time{
		time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC):  day(2026, 11, 1),  // 00:00 on the 16th there
		time.Date(2026, 11, 16, 2, 59, 0, 0, time.UTC): day(2026, 10, 16), // still the 15th
		time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC):   day(2026, 11, 16),
		time.Date(2027, 1, 10, 12, 0, 0, 0, time.UTC):  day(2026, 12, 16),
		time.Date(2028, 3, 1, 3, 0, 0, 0, time.UTC):    day(2028, 2, 16),
	} {
		got := closes.LastOver(at, closes.Fortnight)
		if !got.Start.Equal(want) || !got.OverAt(at) {
			t.Errorf("%s: %s (over %v), want %s", at, got.Start, got.OverAt(at), want)
		}
	}
}
