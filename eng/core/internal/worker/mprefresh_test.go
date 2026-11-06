package worker_test

import (
	"context"
	"crypto/rand"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago/mpfake"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

// The daily job renews the connected tenant whose token is close to
// expiring, leaves the others alone, and does nothing without an app.
func TestMPRefreshRenewsWhatIsDue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	kr, _ := crypto.NewKeyring(master)
	fake := mpfake.New()
	t.Cleanup(fake.Close)
	connected, idle := uuid.New(), uuid.New()
	owner := uuid.New()
	err := pgx.BeginFunc(ctx, e.db.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, 'molino-mp-a', 'A'), ($2, 'molino-mp-b', 'B')`, connected, idle); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into users (id, email) values ($1, 'o@mp.test')`, owner); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into memberships (user_id, tenant_id, role) values ($1, $2, 'owner')`, owner, connected); err != nil {
			return err
		}
		for _, id := range []uuid.UUID{connected, idle} {
			if err := kr.EnsureTenantKey(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cfg := mercadopago.Config{ClientID: fake.ClientID, ClientSecret: fake.ClientSecret, RedirectURI: fake.RedirectURI, APIURL: fake.URL}
	svc := &mercadopago.Service{DB: e.db, Keyring: kr, Config: cfg, HTTP: fake.Client(), Now: clock}
	octx := db.WithScope(ctx, db.Scope{TenantID: connected, UserID: owner})
	u, _, err := svc.Authorize(octx)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	code, _ := fake.Grant(q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("code_challenge_method"), 5)
	if _, err := svc.Connect(octx, code, q.Get("state")); err != nil {
		t.Fatal(err)
	}

	now = now.Add(170 * 24 * time.Hour)
	job := &river.Job[worker.MPRefreshArgs]{Args: worker.MPRefreshFor(now)}
	unset := &worker.MPRefreshWorker{DB: e.db, Keyring: kr, HTTP: fake.Client(), Now: clock}
	before := fake.Requests()
	if err := unset.Work(ctx, job); err != nil || fake.Requests() != before {
		t.Fatalf("without an app: %v, %d calls", err, fake.Requests()-before)
	}
	w := &worker.MPRefreshWorker{DB: e.db, Keyring: kr, Config: cfg, HTTP: fake.Client(), Now: clock}
	if err := w.Work(ctx, job); err != nil {
		t.Fatal(err)
	}
	if n := fake.Requests() - before; n != 1 {
		t.Fatalf("%d renewals, want 1 (only the connected tenant)", n)
	}
	var refreshed *time.Time
	if err := e.db.Pool.QueryRow(ctx, `select last_refresh_at from mercadopago.connections where tenant_id = $1`, connected).Scan(&refreshed); err != nil || refreshed == nil {
		t.Fatalf("not renewed: %v", err)
	}
}
