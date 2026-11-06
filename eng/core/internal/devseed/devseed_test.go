package devseed_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/devseed"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const fixtures = "../ledger/testdata"

func TestGuardRefusesAnythingButDev(t *testing.T) {
	for _, ok := range [][2]string{{"", ""}, {"dev", ""}, {"dev", "sim"}, {"", "sim"}} {
		if err := devseed.Guard(ok[0], ok[1]); err != nil {
			t.Errorf("APP_ENV=%q ARCA_ENV=%q refused: %v", ok[0], ok[1], err)
		}
	}
	for _, bad := range [][2]string{{"staging", ""}, {"prod", ""}, {"production", ""}, {"dev", "homologacion"}, {"dev", "produccion"}, {"prod", "sim"}} {
		if err := devseed.Guard(bad[0], bad[1]); err == nil {
			t.Errorf("APP_ENV=%q ARCA_ENV=%q accepted", bad[0], bad[1])
		}
	}
}

func TestSecretsAreStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	a, err := devseed.LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := devseed.LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.MasterKey) != string(b.MasterKey) || len(a.MasterKey) != 32 || a.Passwords["owner@alfa.test"] != b.Passwords["owner@alfa.test"] {
		t.Fatal("secrets changed between runs")
	}
	if len(a.Passwords) != len(devseed.Users) {
		t.Fatalf("%d passwords for %d users", len(a.Passwords), len(devseed.Users))
	}
	info, err := os.Stat(filepath.Join(dir, "dev-secrets.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets file mode: %v %v", info.Mode(), err)
	}
}

type counts map[string]int

func count(t *testing.T, d *db.DB) counts {
	t.Helper()
	ctx := context.Background()
	out := counts{}
	// Identity tables through the owner pool (test only); module tables per
	// tenant under RLS, summed.
	for name, q := range map[string]string{
		"tenants":     `select count(*) from tenants where slug like '%-dev'`,
		"users":       `select count(*) from users`,
		"memberships": `select count(*) from memberships`,
		"tenant_keys": `select count(*) from tenant_keys`,
	} {
		var n int
		if err := d.Pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out[name] = n
	}
	for _, tn := range devseed.Tenants {
		tctx := jobs.TenantContext(ctx, tn.ID)
		err := d.InTenant(tctx, func(tx pgx.Tx) error {
			for name, q := range map[string]string{
				"accounts":  `select count(*) from ledger.accounts`,
				"movements": `select count(*) from ledger.movements`,
				"items":     `select count(*) from matching.items`,
				"runs":      `select count(*) from matching.runs`,
				"matches":   `select count(*) from matching.matches`,
			} {
				var n int
				if err := tx.QueryRow(tctx, q).Scan(&n); err != nil {
					return err
				}
				out[name] += n
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestSeedIsIdempotentAndFillsEveryTray(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	s, err := devseed.LoadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := crypto.NewKeyring(s.MasterKey)

	if err := devseed.Seed(ctx, d, kr, s, fixtures); err != nil {
		t.Fatal(err)
	}
	first := count(t, d)
	want := counts{"tenants": 2, "users": 6, "memberships": 8, "tenant_keys": 2, "accounts": 2, "movements": 20, "items": 6, "runs": 2}
	for k, v := range want {
		if first[k] != v {
			t.Errorf("%s: %d, want %d", k, first[k], v)
		}
	}
	if err := devseed.Seed(ctx, d, kr, s, fixtures); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second := count(t, d); len(second) != len(first) {
		t.Fatalf("counts differ: %v vs %v", first, second)
	} else {
		for k := range first {
			if first[k] != second[k] {
				t.Errorf("%s: %d after the first seed, %d after the second", k, first[k], second[k])
			}
		}
	}

	// Every tray has something to show, in each tenant. Suggested is the
	// operator's only (F20), so the trays are read as the seeded operator,
	// who is a member of both tenants; any error fails the test.
	var operator uuid.UUID
	if err := d.Pool.QueryRow(ctx, `select id from users where email = 'operator@tramo.test'`).Scan(&operator); err != nil {
		t.Fatal(err)
	}
	store := &matching.PGStore{DB: d}
	for _, tn := range devseed.Tenants {
		tctx := db.WithScope(ctx, db.Scope{TenantID: tn.ID, UserID: operator, Role: identity.RoleOperator})
		auto, err := store.Undecided(tctx, matching.Auto, 100)
		if err != nil {
			t.Fatal(err)
		}
		sugM, err := store.Undecided(tctx, matching.Suggested, 100)
		if err != nil {
			t.Fatal(err)
		}
		sugP, err := store.LatestPending(tctx, matching.Suggested, 100)
		if err != nil {
			t.Fatal(err)
		}
		exc, err := store.LatestPending(tctx, matching.Exception, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(auto) == 0 || len(sugM)+len(sugP) == 0 || len(exc) == 0 {
			t.Errorf("%s trays: auto %d, suggested %d+%d, exception %d", tn.Slug, len(auto), len(sugM), len(sugP), len(exc))
		}
	}
}

func TestSeededUsersLogInWithThePrintedPasswords(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()
	s, _ := devseed.LoadSecrets(t.TempDir())
	kr, _ := crypto.NewKeyring(s.MasterKey)
	if err := devseed.Seed(ctx, d, kr, s, fixtures); err != nil {
		t.Fatal(err)
	}
	signer, _ := identity.NewSigner(s.JWTSeed, "dev")
	ids, err := identity.NewService(d, signer, kr, identity.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range devseed.Users {
		sess, err := ids.Login(ctx, u.Email, s.Passwords[u.Email], nil)
		if err != nil {
			t.Errorf("%s: %v", u.Email, err)
			continue
		}
		if _, ok := u.Roles[slugOf(sess.Principal.TenantID.String())]; !ok {
			t.Errorf("%s logged into an unexpected tenant", u.Email)
		}
	}
	if _, err := ids.Login(ctx, "owner@alfa.test", "wrong password", nil); err == nil {
		t.Fatal("wrong password accepted")
	}
}

func slugOf(id string) string {
	for _, t := range devseed.Tenants {
		if t.ID.String() == id {
			return t.Slug
		}
	}
	return ""
}
