package devseed_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	want := counts{"tenants": 2, "users": 6, "memberships": 8, "tenant_keys": 2, "accounts": 2, "movements": 20, "items": 10, "runs": 2}
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
		handLink(t, store, tctx, tn, exc, sugM, auto)
	}
}

// The case frontend and qa link by hand: the 45.000,00 deposit is in
// Exceptions with no candidate, the two August items are open and add up
// to it exactly, no match proposes them, and linking the deposit to both
// goes through.
func handLink(t *testing.T, store *matching.PGStore, ctx context.Context, tn devseed.Tenant, exc []matching.PendingRow, sug, auto []matching.Match) {
	t.Helper()
	var deposit *matching.PendingRow
	for i := range exc {
		if exc[i].Amount == devseed.HandLinkAmount {
			deposit = &exc[i]
		}
	}
	if deposit == nil {
		t.Fatalf("%s: no %d movement in Exceptions", tn.Slug, devseed.HandLinkAmount)
	}
	if len(deposit.Candidates) != 0 {
		t.Fatalf("%s: the deposit has candidates: %+v", tn.Slug, deposit.Candidates)
	}
	open, _, err := store.OpenItems(ctx, "2026-08", "in", 100)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	var sum int64
	for _, it := range open {
		for _, want := range devseed.HandLinkItems {
			if it.SourceID == want.SourceID {
				ids = append(ids, it.ID.String())
				sum += int64(it.Amount)
			}
		}
	}
	if len(ids) != 2 || sum != devseed.HandLinkAmount {
		t.Fatalf("%s: open August items %v add up to %d, want 2 adding up to %d", tn.Slug, ids, sum, devseed.HandLinkAmount)
	}
	for _, m := range append(sug, auto...) {
		for _, item := range m.ItemIDs {
			if slices.Contains(ids, item) {
				t.Fatalf("%s: match %s already proposes a hand link item", tn.Slug, m.ID)
			}
		}
	}
	// Exceptions are the client's tray: the tenant's owner links.
	var owner uuid.UUID
	if err := store.DB.Pool.QueryRow(ctx,
		`select user_id from memberships where tenant_id = $1 and role = 'owner'`, tn.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	ctx = db.WithScope(ctx, db.Scope{TenantID: tn.ID, UserID: owner, Role: identity.RoleOwner})
	dec, _, err := store.DecideMovement(ctx, matching.MovementDecisionInput{
		Key: uuid.NewString(), MovementID: deposit.MovementID, Action: matching.MovementLink, ItemIDs: ids})
	if err != nil || dec.MatchID == "" {
		t.Fatalf("%s: link by hand: %+v %v", tn.Slug, dec, err)
	}
}

// A seed that fails must stop make dev, not leave a half filled database
// that looks fine. Two real failures: a statement the ingester refuses
// (ledger) and a missing matching table (matching).
func TestSeedErrorsAreFatal(t *testing.T) {
	ctx := context.Background()
	s, err := devseed.LoadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kr, _ := crypto.NewKeyring(s.MasterKey)

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "bancor_ok.csv"), []byte("not a statement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := devseed.Seed(ctx, dbtest.New(t), kr, s, broken); err == nil || !strings.Contains(err.Error(), "ledger") {
		t.Errorf("a refused statement: %v, want a ledger error", err)
	}

	d := dbtest.New(t)
	if _, err := d.Pool.Exec(ctx, `drop table matching.runs cascade`); err != nil {
		t.Fatal(err)
	}
	if err := devseed.Seed(ctx, d, kr, s, fixtures); err == nil || !strings.Contains(err.Error(), "matching") {
		t.Errorf("a failing matching run: %v, want a matching error", err)
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
