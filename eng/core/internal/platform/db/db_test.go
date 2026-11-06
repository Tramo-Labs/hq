package db_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// seed creates two tenants with one owner each through the system path.
func seed(t *testing.T, d *db.DB) (a, b db.Scope) {
	t.Helper()
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		for i, s := range []*db.Scope{&a, &b} {
			s.TenantID, s.UserID = uuid.New(), uuid.New()
			if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, $2)`,
				s.TenantID, []string{"alfa", "beta"}[i]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into users (id, email, password_hash) values ($1, $2, 'secret-hash')`,
				s.UserID, []string{"a@x.test", "b@x.test"}[i]); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'owner')`,
				s.UserID, s.TenantID); err != nil {
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

func TestTenantCannotSeeAnotherTenant(t *testing.T) {
	d := dbtest.New(t)
	a, b := seed(t, d)
	ctx := db.WithScope(context.Background(), a)

	err := d.InTenant(ctx, func(tx pgx.Tx) error {
		// No WHERE clause on purpose: RLS is what filters.
		var tenants, members, users int
		if err := tx.QueryRow(ctx, `select (select count(*) from tenants), (select count(*) from memberships), (select count(*) from users)`).
			Scan(&tenants, &members, &users); err != nil {
			return err
		}
		if tenants != 1 || members != 1 || users != 1 {
			t.Errorf("tenant a sees tenants=%d memberships=%d users=%d, want 1 each", tenants, members, users)
		}
		var name string
		err := tx.QueryRow(ctx, `select name from tenants where id = $1`, b.TenantID).Scan(&name)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("reading tenant b by id: got %q, %v; want no rows", name, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTenantCannotWriteIntoAnotherTenant(t *testing.T) {
	d := dbtest.New(t)
	a, b := seed(t, d)
	ctx := db.WithScope(context.Background(), a)
	err := d.InTenant(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'viewer')`, a.UserID, b.TenantID)
		return err
	})
	if err == nil {
		t.Fatal("insert of a membership into another tenant succeeded")
	}
	err = d.InTenant(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update tenants set name = 'pwned' where id = $1`, b.TenantID)
		if err == nil && tag.RowsAffected() != 0 {
			t.Errorf("updated %d rows of another tenant", tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTenantRoleCannotReadSecrets(t *testing.T) {
	d := dbtest.New(t)
	a, _ := seed(t, d)
	ctx := db.WithScope(context.Background(), a)
	err := d.InTenant(ctx, func(tx pgx.Tx) error {
		var h string
		return tx.QueryRow(ctx, `select password_hash from users where id = $1`, a.UserID).Scan(&h)
	})
	if err == nil {
		t.Fatal("tramo_app read users.password_hash")
	}
	err = d.InTenant(ctx, func(tx pgx.Tx) error {
		var n int
		return tx.QueryRow(ctx, `select count(*) from refresh_tokens`).Scan(&n)
	})
	if err == nil {
		t.Fatal("tramo_app read refresh_tokens")
	}
}

// The role of the scope reaches the database as app.role, for policies and
// triggers that need it.
func TestInTenantSetsRole(t *testing.T) {
	d := dbtest.New(t)
	a, _ := seed(t, d)
	a.Role = "viewer"
	ctx := db.WithScope(context.Background(), a)
	var role, tenant string
	err := d.InTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select current_setting('app.role', true), current_setting('app.tenant_id', true)`).Scan(&role, &tenant)
	})
	if err != nil {
		t.Fatal(err)
	}
	if role != "viewer" || tenant != a.TenantID.String() {
		t.Fatalf("app.role=%q app.tenant_id=%q", role, tenant)
	}
	got, _ := db.ScopeFrom(ctx)
	if got.Role != "viewer" {
		t.Fatalf("ScopeFrom lost the role: %+v", got)
	}
}

func TestInTenantWithoutScopeFails(t *testing.T) {
	d := dbtest.New(t)
	err := d.InTenant(context.Background(), func(pgx.Tx) error { return nil })
	if !errors.Is(err, db.ErrNoScope) {
		t.Fatalf("got %v, want ErrNoScope", err)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	d := dbtest.New(t)
	a, _ := seed(t, d)
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `insert into audit_log (tenant_id, action, subject) values ($1, 'test', 'x:1')`, a.TenantID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The raw pool is the owner (a superuser in tests): only the trigger
	// stands in the way.
	for _, q := range []string{`update audit_log set action = 'other'`, `delete from audit_log`} {
		if _, err := d.Pool.Exec(ctx, q); err == nil {
			t.Errorf("%q succeeded as owner", q)
		}
	}
}

func TestApplicationRolesCannotBypassRLS(t *testing.T) {
	d := dbtest.New(t)
	rows, err := d.Pool.Query(context.Background(),
		`select rolname, rolsuper, rolbypassrls, rolcanlogin from pg_roles where rolname = any($1)`,
		[]string{db.AppRole, db.SystemRole})
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var super, bypass, login bool
		if err := rows.Scan(&name, &super, &bypass, &login); err != nil {
			t.Fatal(err)
		}
		seen++
		if super || bypass || login {
			t.Errorf("%s: rolsuper=%v rolbypassrls=%v rolcanlogin=%v, want all false", name, super, bypass, login)
		}
	}
	if seen != 2 {
		t.Fatalf("found %d of the 2 application roles", seen)
	}
}

// The roles may already exist with dangerous attributes (created by hand, by
// an older migration, by another app on the same server). Migrating must
// leave them NOSUPERUSER NOBYPASSRLS anyway.
//
// NEVER mark this test t.Parallel(): it makes server wide roles SUPERUSER for
// a moment and restores them in cleanup, so any test running at the same
// time would run with dangerous roles and could pass for the wrong reason.
func TestMigrationFixesPreexistingRoleAttributes(t *testing.T) {
	d := dbtest.NewEmpty(t)
	ctx := context.Background()
	roles := []string{db.AppRole, db.SystemRole}
	restore := func() {
		for _, r := range roles {
			_, _ = d.Pool.Exec(ctx, "alter role "+r+" nosuperuser nobypassrls nologin")
		}
	}
	t.Cleanup(restore) // roles are server wide: never leave them dangerous
	for _, r := range roles {
		// Both roles exist already (the template migrated them); make them
		// as bad as they can be before this database migrates.
		if _, err := d.Pool.Exec(ctx, "alter role "+r+" superuser bypassrls login"); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range roles {
		var super, bypass, login bool
		if err := d.Pool.QueryRow(ctx, `select rolsuper, rolbypassrls, rolcanlogin from pg_roles where rolname = $1`, r).
			Scan(&super, &bypass, &login); err != nil {
			t.Fatal(err)
		}
		if super || bypass || login {
			t.Errorf("%s after migrating: rolsuper=%v rolbypassrls=%v rolcanlogin=%v, want all false", r, super, bypass, login)
		}
	}
}

// Production runs the api as tramo_runtime (migration 00004, ADR 0005): a
// login role that owns nothing. Its raw pool reaches no table; only the two
// role switches work.
//
// NEVER t.Parallel(): it creates a server wide role.
func TestRuntimeLoginRoleReachesDataOnlyThroughTheRoleSwitch(t *testing.T) {
	admin := dbtest.NewEmpty(t)
	ctx := context.Background()
	name := dbtest.DatabaseName(t, admin)
	if _, err := admin.Pool.Exec(ctx, `create role tramo_runtime login password 'test-only' superuser bypassrls`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Pool.Exec(ctx, `revoke connect on database `+name+` from tramo_runtime`)
		_, _ = admin.Pool.Exec(ctx, `drop role if exists tramo_runtime`)
	})
	// Created dangerous on purpose: the migration must strip it.
	if err := admin.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var super, bypass bool
	if err := admin.Pool.QueryRow(ctx, `select rolsuper, rolbypassrls from pg_roles where rolname = 'tramo_runtime'`).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatalf("tramo_runtime after migrating: rolsuper=%v rolbypassrls=%v", super, bypass)
	}

	rt, err := db.Open(ctx, dbtest.URLAs(name, "tramo_runtime", "test-only"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	a, b := seed(t, rt) // through System: tramo_system may write identity tables

	// The raw pool, a stray query: no privilege on any table.
	for _, q := range []string{`select count(*) from tenants`, `select count(*) from users`, `select count(*) from audit_log`} {
		var n int
		if err := rt.Pool.QueryRow(ctx, q).Scan(&n); err == nil {
			t.Errorf("raw pool as tramo_runtime: %q returned %d rows", q, n)
		}
	}
	// The tenant path still works and is scoped.
	actx := db.WithScope(ctx, a)
	err = rt.InTenant(actx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(actx, `select count(*) from tenants`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("tenant a sees %d tenants", n)
		}
		var other string
		err := tx.QueryRow(actx, `select name from tenants where id = $1`, b.TenantID).Scan(&other)
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("tenant a read tenant b: %q %v", other, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every table, including the ones other modules add later, must have row
// level security enabled and forced.
func TestEveryTableHasForcedRLS(t *testing.T) {
	d := dbtest.New(t)
	// Every schema except Postgres' own: modules may live in their own
	// schemas (ledger, documents, matching, ...).
	rows, err := d.Pool.Query(context.Background(), `
		select n.nspname || '.' || c.relname, c.relrowsecurity, c.relforcerowsecurity
		from pg_class c join pg_namespace n on n.oid = c.relnamespace
		where n.nspname not in ('pg_catalog', 'information_schema', 'pg_toast')
		  and n.nspname not like 'pg_temp_%' and n.nspname not like 'pg_toast_temp_%'
		  and c.relkind in ('r', 'p')
		  and not (n.nspname = 'public' and c.relname = 'goose_db_version')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		var rls, force bool
		if err := rows.Scan(&name, &rls, &force); err != nil {
			t.Fatal(err)
		}
		n++
		if !rls || !force {
			t.Errorf("table %s: row security enabled=%v forced=%v", name, rls, force)
		}
	}
	if n < 7 {
		t.Fatalf("only %d tables found", n)
	}
}

// A business table a module adds later is invisible to the system role
// unless a migration grants it explicitly.
func TestSystemRoleCannotReachBusinessTables(t *testing.T) {
	d := dbtest.New(t)
	a, _ := seed(t, d)
	ctx := context.Background()
	// What a module migration looks like (default privileges reach tramo_app only).
	for _, q := range []string{
		`create table movements (id bigint generated always as identity primary key, tenant_id uuid not null references tenants(id), amount_cents bigint not null)`,
		`alter table movements enable row level security`,
		`alter table movements force row level security`,
		`create policy movements_tenant on movements to tramo_app using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant())`,
	} {
		if _, err := d.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	actx := db.WithScope(ctx, a)
	if err := d.InTenant(actx, func(tx pgx.Tx) error {
		_, err := tx.Exec(actx, `insert into movements (tenant_id, amount_cents) values ($1, 100)`, a.TenantID)
		return err
	}); err != nil {
		t.Fatalf("tenant insert: %v", err)
	}
	err := d.System(ctx, func(tx pgx.Tx) error {
		var n int
		return tx.QueryRow(ctx, `select count(*) from movements`).Scan(&n)
	})
	if err == nil {
		t.Fatal("tramo_system read a business table")
	}
}
