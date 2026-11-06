package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

var fastArgon = identity.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1}

// F7: the first operator comes from the command line, never from HTTP.
func TestCreateOperatorCommand(t *testing.T) {
	ctx := context.Background()
	d := dbtest.New(t)
	env := map[string]string{"DATABASE_URL": dbtest.URL(dbtest.DatabaseName(t, d))}
	getenv := func(k string) string { return env[k] }
	var tenant uuid.UUID
	if err := d.Pool.QueryRow(ctx, `insert into tenants (slug, name) values ('molino-cli', 'Molino CLI') returning id`).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		err := createOperator(ctx, args, strings.NewReader(stdin), &out, getenv, fastArgon)
		return out.String(), err
	}
	args := []string{"-email", "ops@tramo.test", "-tenant", tenant.String(), "-password-stdin"}

	// The password only comes from stdin.
	if _, err := run("a long enough password\n", "-email", "ops@tramo.test", "-tenant", tenant.String()); err == nil {
		t.Fatal("ran without -password-stdin")
	}
	if _, err := run("short\n", args...); err == nil {
		t.Fatal("accepted a short password")
	}

	out, err := run("a long enough password\n", args...)
	if err != nil || !strings.Contains(out, "operator added") || strings.Contains(out, "a long enough password") {
		t.Fatalf("first run: %v %q", err, out)
	}
	var role, hash string
	if err := d.Pool.QueryRow(ctx, `select m.role::text, u.password_hash from memberships m join users u on u.id = m.user_id
		where u.email = 'ops@tramo.test' and m.tenant_id = $1`, tenant).Scan(&role, &hash); err != nil || role != "operator" {
		t.Fatalf("membership: %q %v", role, err)
	}
	if ok, err := identity.VerifyPassword(hash, "a long enough password"); !ok || err != nil {
		t.Fatalf("password not set from stdin: %v", err)
	}

	// Idempotent: a second run changes nothing, not even the password.
	out, err = run("another long password\n", args...)
	if err != nil || !strings.Contains(out, "nothing changed") {
		t.Fatalf("second run: %v %q", err, out)
	}
	if err := d.Pool.QueryRow(ctx, `select password_hash from users where email = 'ops@tramo.test'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := identity.VerifyPassword(hash, "a long enough password"); !ok {
		t.Fatal("password changed")
	}
	var audits int
	var req string
	if err := d.Pool.QueryRow(ctx, `select count(*), max(request_id) from audit_log
		where tenant_id = $1 and action = 'identity.operator_created' and user_id is null and ip is null`, tenant).Scan(&audits, &req); err != nil || audits != 1 || !strings.HasPrefix(req, "cli-") {
		t.Fatalf("audit: %d %q %v", audits, req, err)
	}

	// Someone already in the tenant with another role is not promoted.
	if _, err := d.Pool.Exec(ctx, `insert into users (email) values ('own@cli.test');
		insert into memberships (user_id, tenant_id, role) select id, '`+tenant.String()+`', 'owner' from users where email = 'own@cli.test'`); err != nil {
		t.Fatal(err)
	}
	if _, err := run("a long enough password\n", "-email", "own@cli.test", "-tenant", tenant.String(), "-password-stdin"); !errors.Is(err, identity.ErrAlreadyOtherRole) {
		t.Fatalf("promoted an owner: %v", err)
	}
	// An unknown tenant is refused.
	if _, err := run("a long enough password\n", "-email", "x@tramo.test", "-tenant", uuid.NewString(), "-password-stdin"); err == nil {
		t.Fatal("unknown tenant accepted")
	}
}
