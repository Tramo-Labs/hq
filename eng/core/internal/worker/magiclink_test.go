package worker_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/jobs"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/mail"
	"github.com/Tramo-Labs/hq/eng/core/internal/worker"
)

// D-17 through the real queue: the api only enqueues; the worker looks the
// email up and sends the link to a known email only. The same request
// enqueued twice (a retried insert) is one job and one link.
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
	known := identity.MagicLinkArgs{Email: "ana@cola.test", RequestID: "req-1", IP: "198.51.100.1"}
	unknown := identity.MagicLinkArgs{Email: "nadie@cola.test", RequestID: "req-2", IP: "198.51.100.1"}
	for _, a := range []identity.MagicLinkArgs{known, unknown} {
		if ok, err := jobs.Insert(ctx, inserter, a); err != nil || !ok {
			t.Fatalf("insert %s: %v %v", a.Email, ok, err)
		}
	}
	if ok, err := jobs.Insert(ctx, inserter, known); err != nil || ok {
		t.Fatalf("the same request enqueued twice: %v %v", ok, err)
	}

	_, events := e.start(t, setup.Workers)
	completed(t, events, identity.MagicLinkArgs{}.Kind(), 2, 15*time.Second)

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
