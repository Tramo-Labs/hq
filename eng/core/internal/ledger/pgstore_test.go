package ledger

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// seedTenants creates two tenants with one owner each, through the system
// path (only tests and identity do this).
func seedTenants(t *testing.T, d *db.DB) (a, b db.Scope) {
	t.Helper()
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		for i, s := range []*db.Scope{&a, &b} {
			s.TenantID, s.UserID = uuid.New(), uuid.New()
			slug := []string{"alfa", "beta"}[i]
			if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, $2)`, s.TenantID, slug); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into users (id, email) values ($1, $2)`, s.UserID, slug+"@example.test"); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'owner')`, s.UserID, s.TenantID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

type pgEnv struct {
	d      *db.DB
	store  *PGStore
	in     *Ingester
	ctx    context.Context
	scope  db.Scope
	acct   uuid.UUID
	ctxB   context.Context
	scopeB db.Scope
}

func newPGEnv(t *testing.T) pgEnv {
	d := dbtest.New(t)
	a, b := seedTenants(t, d)
	s := &PGStore{DB: d}
	e := pgEnv{d: d, store: s, in: &Ingester{Store: s}, scope: a, scopeB: b,
		ctx: db.WithScope(context.Background(), a), ctxB: db.WithScope(context.Background(), b)}
	var err error
	if e.acct, err = s.CreateAccount(e.ctx, Bancor, "", "Cuenta corriente"); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e pgEnv) upload(t *testing.T, ctx context.Context, sc db.Scope, acct uuid.UUID, file string) Result {
	t.Helper()
	r, err := e.in.Ingest(ctx, Upload{TenantID: sc.TenantID.String(), AccountID: acct.String(), Bank: Bancor,
		Filename: file, Data: fixture(t, file)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func count(t *testing.T, d *db.DB, ctx context.Context, query string) int {
	t.Helper()
	var n int
	if err := d.InTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, query).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPGIngestLifecycle(t *testing.T) {
	e := newPGEnv(t)
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_part1.csv"); r.Status != Accepted || r.New != 7 {
		t.Fatalf("part1 %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_part2.csv"); r.Status != Accepted || r.New != 3 || r.AlreadyPresent != 3 {
		t.Fatalf("part2 %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_part2.csv"); r.Status != Duplicate {
		t.Fatalf("re-upload %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_nov_gap.csv"); r.Reason != ReasonChainGap {
		t.Fatalf("gap %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_bad_balance.csv"); r.Reason != ReasonBalance {
		t.Fatalf("bad balance %+v", r)
	}
	if r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_nov.csv"); r.Status != Accepted {
		t.Fatalf("november %+v", r)
	}
	if n := count(t, e.d, e.ctx, `select count(*) from ledger.movements`); n != 12 {
		t.Errorf("%d movements stored; want 12", n)
	}
	if n := count(t, e.d, e.ctx, `select count(*) from ledger.files where status = 'quarantined' and reason is not null`); n != 2 {
		t.Errorf("%d quarantined files; want 2", n)
	}
	// account created, three statements accepted, two files quarantined
	if n := count(t, e.d, e.ctx, `select count(*) from audit_log where action like 'ledger.%'`); n != 6 {
		t.Errorf("%d audit entries; want 6", n)
	}

	day6 := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	mv, err := e.store.MovementsBetween(e.ctx, day6, day6)
	if err != nil || len(mv) != 3 {
		t.Fatalf("day 6: %d movements, %v", len(mv), err)
	}
	if mv[0].ID == uuid.Nil || mv[0].Key == "" || mv[0].Balance == nil {
		t.Errorf("stored movement %+v", mv[0])
	}
}

func TestPGMercadoPagoFeesRoundTrip(t *testing.T) {
	e := newPGEnv(t)
	acct, err := e.store.CreateAccount(e.ctx, MercadoPago, "", "Mercado Pago")
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.in.Ingest(e.ctx, Upload{TenantID: e.scope.TenantID.String(), AccountID: acct.String(), Bank: MercadoPago,
		Data: fixture(t, "mercadopago_ok.csv")})
	if err != nil || r.Status != Accepted {
		t.Fatalf("%v %+v", err, r)
	}
	oct1 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mv, err := e.store.MovementsBetween(e.ctx, oct1, oct1)
	if err != nil || len(mv) != 2 {
		t.Fatalf("%d %v", len(mv), err)
	}
	var first *StoredMovement
	for i := range mv {
		if mv[i].Reference == "90000000001" {
			first = &mv[i]
		}
	}
	if first == nil || first.Gross == nil || len(first.Fees) != 2 || first.Fees[0].Kind != "mp_fee" || first.Fees[0].Amount != -41000 {
		t.Errorf("fees lost on the way: %+v", first)
	}
}

// Spec: a tenant does not see another tenant's statements, even when the
// code asks wrong.
func TestPGTenantIsolation(t *testing.T) {
	e := newPGEnv(t)
	r := e.upload(t, e.ctx, e.scope, e.acct, "bancor_ok.csv")
	if r.Status != Accepted {
		t.Fatal(r)
	}
	// Tenant B, no WHERE on tenant anywhere: RLS is what filters.
	for _, table := range []string{"ledger.accounts", "ledger.files", "ledger.statements", "ledger.movements"} {
		if n := count(t, e.d, e.ctxB, "select count(*) from "+table); n != 0 {
			t.Errorf("tenant b sees %d rows of %s", n, table)
		}
	}
	seen, err := e.store.FileSeen(e.ctxB, e.scopeB.TenantID.String(), r.FileHash)
	if err != nil || seen {
		t.Errorf("tenant b sees a's file: %v %v", seen, err)
	}
	// B cannot hang rows on A's account and statement, nor write rows as A.
	var aStatement uuid.UUID
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select id from ledger.statements limit 1`).Scan(&aStatement)
	}); err != nil {
		t.Fatal(err)
	}
	err = e.d.InTenant(e.ctxB, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctxB, `insert into ledger.movements (tenant_id, account_id, statement_id, key, line, date, amount, description)
			values (app_current_tenant(), $1, $2, 'k', 1, '2026-10-01', 1, 'x')`, e.acct, aStatement)
		return err
	})
	if err == nil {
		t.Error("tenant b attached a movement to tenant a's account and statement")
	}
	err = e.d.InTenant(e.ctxB, func(tx pgx.Tx) error {
		_, err := tx.Exec(e.ctxB, `insert into ledger.accounts (tenant_id, bank, name) values ($1, 'bancor', 'x')`, e.scope.TenantID)
		return err
	})
	if err == nil {
		t.Error("tenant b wrote an account for tenant a")
	}
	// The same file is a new file for another tenant.
	acctB, err := e.store.CreateAccount(e.ctxB, Bancor, "", "B")
	if err != nil {
		t.Fatal(err)
	}
	if rb := e.upload(t, e.ctxB, e.scopeB, acctB, "bancor_ok.csv"); rb.Status != Accepted {
		t.Errorf("tenant b upload %+v", rb)
	}
}

// Every table of the datos schemas has RLS enabled and forced, and a
// tenant policy for tramo_app. (Forcing cannot be shown with a query here:
// the embedded server's owner is a superuser, and superusers bypass RLS.)
func TestPGEveryTableForcesRLS(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select n.nspname || '.' || c.relname, c.relrowsecurity, c.relforcerowsecurity,
			       exists (select 1 from pg_policies p where p.schemaname = n.nspname and p.tablename = c.relname
			               and 'tramo_app' = any(p.roles) and p.qual like '%app_current_tenant()%')
			  from pg_class c join pg_namespace n on n.oid = c.relnamespace
			 where n.nspname in ('ledger', 'documents', 'matching') and c.relkind = 'r'
			 order by 1`)
		if err != nil {
			return err
		}
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var name string
			var enabled, forced, policy bool
			if err := rows.Scan(&name, &enabled, &forced, &policy); err != nil {
				return err
			}
			seen++
			if !enabled || !forced || !policy {
				t.Errorf("%s: rls %v, forced %v, tenant policy %v", name, enabled, forced, policy)
			}
		}
		if seen != 10 {
			t.Errorf("%d tables in the datos schemas; want 10", seen)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPGScopeMismatch(t *testing.T) {
	e := newPGEnv(t)
	if _, err := e.store.FileSeen(e.ctx, e.scopeB.TenantID.String(), "00"); !errors.Is(err, ErrScopeMismatch) {
		t.Errorf("other tenant: %v", err)
	}
	if _, err := e.store.FileSeen(context.Background(), e.scope.TenantID.String(), "00"); !errors.Is(err, db.ErrNoScope) {
		t.Errorf("no scope: %v", err)
	}
}
