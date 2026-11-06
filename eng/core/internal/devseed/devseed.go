// Package devseed fills a local development database with synthetic data:
// two tenants, one user per role, a Bancor statement ingested with ledger's
// own ingester and a matching run with matching's own engine, so every tray
// has items. It is idempotent: running it again changes nothing.
//
// Dev only. Seed refuses any environment but dev (Guard). Passwords are
// random per data directory, kept in a file there (never in the repo) and
// printed by `make dev`.
package devseed

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/matching"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
)

// Guard refuses anything that is not a dev environment.
func Guard(appEnv, arcaEnv string) error {
	if appEnv != "" && appEnv != "dev" {
		return fmt.Errorf("devseed: APP_ENV=%q; make dev only runs with APP_ENV=dev", appEnv)
	}
	if arcaEnv != "" && arcaEnv != "sim" {
		return fmt.Errorf("devseed: ARCA_ENV=%q; make dev only runs against the ARCA simulator", arcaEnv)
	}
	return nil
}

// Secrets are generated once per data directory and reused: the master key
// keeps tenant data keys readable across restarts, the JWT seed keeps
// sessions alive, the passwords stay the ones printed last time.
type Secrets struct {
	MasterKey []byte            `json:"masterKey"`
	JWTSeed   []byte            `json:"jwtSeed"`
	Passwords map[string]string `json:"passwords"` // email -> password
}

// LoadSecrets reads dir/dev-secrets.json, creating it (mode 0600) the first
// time.
func LoadSecrets(dir string) (Secrets, error) {
	const name = "dev-secrets.json"
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Secrets{}, err
	}
	// Files are read and written through a root scoped to dir, so a name
	// can never escape it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Secrets{}, err
	}
	defer root.Close()
	if b, err := root.ReadFile(name); err == nil {
		var s Secrets
		if err := json.Unmarshal(b, &s); err != nil {
			return Secrets{}, fmt.Errorf("devseed: %s is corrupt: %w", path, err)
		}
		return s, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Secrets{}, err
	}
	s := Secrets{MasterKey: random(32), JWTSeed: random(32), Passwords: map[string]string{}}
	for _, u := range Users {
		s.Passwords[u.Email] = "dev-" + base64.RawURLEncoding.EncodeToString(random(12))
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Secrets{}, err
	}
	return s, root.WriteFile(name, b, 0o600)
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Tenant is a seeded tenant.
type Tenant struct {
	ID         uuid.UUID
	Slug, Name string
	CUIT       int64
}

// Tenants are the two seeded tenants (synthetic CUITs, valid check digits).
var Tenants = []Tenant{
	{ID: uuid.MustParse("00000000-0000-4000-8000-00000000a1fa"), Slug: "alfa-dev", Name: "Molino Alfa (dev)", CUIT: 30000000015},
	{ID: uuid.MustParse("00000000-0000-4000-8000-00000000be7a"), Slug: "beta-dev", Name: "Molino Beta (dev)", CUIT: 30000000023},
}

// User is a seeded user with its memberships (tenant slug -> role).
type User struct {
	Email string
	Roles map[string]string
}

// Users: one per role, the accountant and the operator over both tenants,
// plus an owner for beta so isolation can be tried by hand.
var Users = []User{
	{Email: "owner@alfa.test", Roles: map[string]string{"alfa-dev": identity.RoleOwner}},
	{Email: "approver@alfa.test", Roles: map[string]string{"alfa-dev": identity.RoleApprover}},
	{Email: "viewer@alfa.test", Roles: map[string]string{"alfa-dev": identity.RoleViewer}},
	{Email: "accountant@estudio.test", Roles: map[string]string{"alfa-dev": identity.RoleAccountant, "beta-dev": identity.RoleAccountant}},
	{Email: "operator@tramo.test", Roles: map[string]string{"alfa-dev": identity.RoleOperator, "beta-dev": identity.RoleOperator}},
	{Email: "owner@beta.test", Roles: map[string]string{"beta-dev": identity.RoleOwner}},
}

// Period is the month of the seeded statement (bancor_ok.csv is October 2026).
const Period = "2026-10"

// Seed applies everything. fixtures is the directory with ledger's test
// fixtures (bancor_ok.csv). Cheap argon2 parameters are fine: dev only.
func Seed(ctx context.Context, d *db.DB, kr *crypto.Keyring, s Secrets, fixtures string) error {
	if err := seedIdentity(ctx, d, kr, s); err != nil {
		return err
	}
	root, err := os.OpenRoot(fixtures)
	if err != nil {
		return fmt.Errorf("devseed: fixtures: %w", err)
	}
	defer root.Close()
	data, err := root.ReadFile("bancor_ok.csv")
	if err != nil {
		return fmt.Errorf("devseed: fixture: %w", err)
	}
	for _, t := range Tenants {
		ctx := jobs.TenantContext(ctx, t.ID)
		if err := seedLedger(ctx, d, t, data); err != nil {
			return fmt.Errorf("devseed: ledger %s: %w", t.Slug, err)
		}
		if err := seedMatching(ctx, d); err != nil {
			return fmt.Errorf("devseed: matching %s: %w", t.Slug, err)
		}
	}
	return nil
}

func seedIdentity(ctx context.Context, d *db.DB, kr *crypto.Keyring, s Secrets) error {
	fast := identity.Argon2Params{Memory: 19 * 1024, Time: 2, Threads: 1}
	return d.System(ctx, func(tx pgx.Tx) error {
		for _, t := range Tenants {
			if _, err := tx.Exec(ctx,
				`insert into tenants (id, slug, name, cuit) values ($1, $2, $3, $4) on conflict (id) do nothing`,
				t.ID, t.Slug, t.Name, t.CUIT); err != nil {
				return err
			}
			if err := kr.EnsureTenantKey(ctx, tx, t.ID); err != nil {
				return err
			}
		}
		slugs := map[string]uuid.UUID{}
		for _, t := range Tenants {
			slugs[t.Slug] = t.ID
		}
		for _, u := range Users {
			hash, err := identity.HashPassword(fast, s.Passwords[u.Email])
			if err != nil {
				return err
			}
			var id uuid.UUID
			// The password follows the secrets file, so it is reset on every
			// run to the one printed.
			if err := tx.QueryRow(ctx,
				`insert into users (email, password_hash) values ($1, $2)
				 on conflict (email) do update set password_hash = excluded.password_hash
				 returning id`, u.Email, hash).Scan(&id); err != nil {
				return err
			}
			for slug, role := range u.Roles {
				if _, err := tx.Exec(ctx,
					`insert into memberships (user_id, tenant_id, role) values ($1, $2, $3) on conflict do nothing`,
					id, slugs[slug], role); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func seedLedger(ctx context.Context, d *db.DB, t Tenant, data []byte) error {
	store := &ledger.PGStore{DB: d}
	accounts, err := store.Accounts(ctx)
	if err != nil {
		return err
	}
	var account uuid.UUID
	for _, a := range accounts {
		if a.Name == "Cuenta corriente Bancor (dev)" {
			account = a.ID
		}
	}
	if account == uuid.Nil {
		if account, err = store.CreateAccount(ctx, ledger.Bancor, "", "Cuenta corriente Bancor (dev)"); err != nil {
			return err
		}
	}
	// The ingester dedups by file hash: a second run answers duplicate.
	res, err := (&ledger.Ingester{Store: store}).Ingest(ctx, ledger.Upload{
		TenantID: t.ID.String(), AccountID: account.String(), Bank: ledger.Bancor,
		Filename: "bancor_ok.csv", Data: data, ReceivedAt: time.Now(),
	})
	if err != nil {
		return err
	}
	if res.Status != ledger.Accepted && res.Status != ledger.Duplicate {
		return fmt.Errorf("statement %s: %s", res.Status, res.Reason)
	}
	return nil
}

// seedItems are invoices made to land in each tray against bancor_ok.csv:
// one exact (automatic), one 75 cents off (suggested, within the rounding
// cap), one for two identical transfers (ambiguous, suggested). Every other
// movement has no candidate (exception).
//
// HandLinkItems are two August invoices of one client (CUIT 30000000074) that add up exactly
// to the cash deposit of 6 October (45.000,00). They are outside the
// matching window, so the deposit stays in Exceptions with no candidate,
// and an operator or the client can link it by hand to both.
var seedItems = append([]matching.ItemIn{
	{SourceID: "dev-inv-1", Kind: "invoice", Date: day(1), Due: day(1), Amount: 25000000, Refs: []string{"0001-00000101"}, Confidence: 1, Party: "30000000031"},
	{SourceID: "dev-inv-2", Kind: "invoice", Date: day(9), Due: day(9), Amount: 31025000, Refs: []string{"0001-00000108"}, Confidence: 1, Party: "30000000058"},
	{SourceID: "dev-inv-3", Kind: "invoice", Date: day(6), Due: day(6), Amount: 10000000, Refs: []string{"0001-00000106"}, Confidence: 1, Party: "30000000066"},
}, HandLinkItems...)

// HandLinkItems: see seedItems.
var HandLinkItems = []matching.ItemIn{
	{SourceID: "dev-inv-4", Kind: "invoice", Date: aug(10), Due: aug(10), Amount: 3000000, Refs: []string{"0001-00000201"}, Confidence: 1, Party: "30000000074"},
	{SourceID: "dev-inv-5", Kind: "invoice", Date: aug(20), Due: aug(20), Amount: 1500000, Refs: []string{"0001-00000202"}, Confidence: 1, Party: "30000000074"},
}

// HandLinkAmount is the deposit those two items add up to (45.000,00).
const HandLinkAmount = 4500000

func day(n int) time.Time { return time.Date(2026, 10, n, 0, 0, 0, 0, time.UTC) }

func aug(n int) time.Time { return time.Date(2026, 8, n, 0, 0, 0, 0, time.UTC) }

func seedMatching(ctx context.Context, d *db.DB) error {
	store := &matching.PGStore{DB: d}
	if _, err := store.UpsertItems(ctx, "devseed", seedItems); err != nil {
		return err
	}
	var runs int
	if err := d.InTenant(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `select count(*) from matching.runs where period = $1`, Period).Scan(&runs)
	}); err != nil {
		return err
	}
	if runs > 0 {
		return nil // one run per period: a second seed does not add a run
	}
	// The same monthly run the matching.run_month job does.
	_, err := store.RunMonth(ctx, Period, matching.DefaultConfig)
	return err
}
