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
	for _, q := range []string{`update audit_log set action = 'other'`, `delete from audit_log`} {
		err := d.System(ctx, func(tx pgx.Tx) error { _, err := tx.Exec(ctx, q); return err })
		if err == nil {
			t.Errorf("%q succeeded even as owner", q)
		}
	}
}
