package worker_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

// D-17 through the real queue and the production request path: the
// request stores the pending request and enqueues a job with its id only;
// the worker takes it, looks the email up and sends the link to a known
// email only. River stores job arguments in clear: the email and the IP
// are never there. The pending rows are gone after the jobs.
func TestMagicLinkJobSendsThroughTheQueue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.Pool.Exec(ctx, `insert into users (email) values ('ana@cola.test')`); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(t.TempDir(), "outbox")
	setup, err := worker.New(e.db, worker.Options{Mailer: &mail.Outbox{Path: outbox}, AppBaseURL: "https://app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	inserter, err := jobs.NewInserter(e.pool)
	if err != nil {
		t.Fatal(err)
	}
	seed, master := make([]byte, 32), make([]byte, 32)
	_, _ = rand.Read(seed)
	_, _ = rand.Read(master)
	signer, _ := identity.NewSigner(seed, "t")
	kr, _ := crypto.NewKeyring(master)
	ids, err := identity.NewService(e.db, signer, kr, identity.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1})
	if err != nil {
		t.Fatal(err)
	}
	ids.Enqueue = func(ctx context.Context, args river.JobArgs) error {
		_, err := jobs.Insert(ctx, inserter, args)
		return err
	}
	for i, email := range []string{"ana@cola.test", "nadie@cola.test"} {
		rctx := httpx.WithMeta(ctx, httpx.Meta{RequestID: "req-" + string(rune('1'+i)), IP: netip.MustParseAddr("198.51.100.1")})
		if err := ids.RequestMagicLink(rctx, email, nil); err != nil {
			t.Fatalf("%s: %v", email, err)
		}
	}

	_, events := e.start(t, setup.Workers)
	completed(t, events, identity.MagicLinkArgs{}.Kind(), 2, 15*time.Second)

	// The queue never holds the email or the IP.
	rows, err := e.db.Pool.Query(ctx, `select args::text from river.river_job where kind = $1`, identity.MagicLinkArgs{}.Kind())
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, a)
	}
	rows.Close()
	if len(stored) != 2 {
		t.Fatalf("%d magic link jobs in river.river_job, want 2", len(stored))
	}
	for _, a := range stored {
		for _, secret := range []string{"ana@cola.test", "nadie@cola.test", "@", "198.51.100.1"} {
			if strings.Contains(a, secret) {
				t.Fatalf("river.river_job args carry %q: %s", secret, a)
			}
		}
	}

	// The pending requests are gone, and the same request enqueued again
	// (a retried insert) is one job: skipped.
	var left int
	if err := e.db.Pool.QueryRow(ctx, `select count(*) from magic_link_requests`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d pending requests left after the jobs (%v)", left, err)
	}
	var again identity.MagicLinkArgs
	if err := json.Unmarshal([]byte(stored[0]), &again); err != nil {
		t.Fatal(err)
	}
	if ok, err := jobs.Insert(ctx, inserter, again); err != nil || ok {
		t.Fatalf("the same request enqueued twice: %v %v", ok, err)
	}

	b, err := os.ReadFile(outbox)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "/ingresar#token=") != 1 || !strings.Contains(string(b), "To: ana@cola.test") ||
		strings.Contains(string(b), "nadie@cola.test") {
		t.Fatalf("outbox: %s", b)
	}
	var links int
	var ip, req string
	if err := e.db.Pool.QueryRow(ctx, `select count(*) from magic_links`).Scan(&links); err != nil || links != 1 {
		t.Fatalf("links: %d %v", links, err)
	}
	// The job's audit row carries the request's id and IP.
	if err := e.db.Pool.QueryRow(ctx,
		`select host(ip), request_id from audit_log where action = 'identity.magic_link_sent'`).Scan(&ip, &req); err != nil ||
		ip != "198.51.100.1" || req != "req-1" {
		t.Fatalf("audit: %q %q %v", ip, req, err)
	}
}

// A request left behind (its enqueue failed) is purged by the daily
// maintenance after a day; a fresh one is not.
func TestStaleMagicLinkRequestsArePurged(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.db.Pool.Exec(ctx, `
		insert into magic_link_requests (email, created_at) values
		  ('viejo@cola.test', now() - interval '25 hours'),
		  ('nuevo@cola.test', now())`); err != nil {
		t.Fatal(err)
	}
	if err := (&worker.MaintenanceWorker{DB: e.db}).Work(ctx, &river.Job[worker.MaintenanceArgs]{Args: worker.MaintenanceFor(time.Now())}); err != nil {
		t.Fatal(err)
	}
	var left []string
	rows, err := e.db.Pool.Query(ctx, `select email from magic_link_requests`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		left = append(left, s)
	}
	rows.Close()
	if len(left) != 1 || left[0] != "nuevo@cola.test" {
		t.Fatalf("left after the purge: %v", left)
	}
}
