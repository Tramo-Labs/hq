// Package dbtest gives tests a real, migrated Postgres without Docker.
//
// One embedded Postgres 17 runs per test binary (per package). Migrations are
// applied once to a template database and every call to New clones it, so
// tests are isolated and fast. Packages that use it must call Main from
// TestMain so the server is stopped:
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }
//
// Ports: 54329 to 54338 by default (go test runs packages in parallel; each
// takes the first free port). Another suite running at the same time sets
// TRAMO_TEST_PG_PORTS, e.g. 54349-54358 for qa. Skipped with -short.
package dbtest

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

const (
	firstPort = 54329
	lastPort  = 54338
	template  = "tramo_template"
)

var (
	once     sync.Once
	startErr error
	server   *embeddedpostgres.EmbeddedPostgres
	port     uint32
	tmpDir   string
	counter  atomic.Int64
)

// Main runs the tests and stops the embedded server afterwards.
func Main(m *testing.M) int {
	code := m.Run()
	if server != nil {
		_ = server.Stop()
	}
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
	}
	return code
}

// URL returns the connection string for a database on the test server.
func URL(name string) string {
	return fmt.Sprintf("postgres://postgres:postgres@localhost:%d/%s?sslmode=disable", port, name)
}

// New returns a pool on a fresh database with every migration applied.
func New(t testing.TB) *db.DB {
	t.Helper()
	if testing.Short() {
		t.Skip("needs embedded postgres; skipped with -short")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatalf("dbtest: %v", startErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := fmt.Sprintf("t_%d_%d", os.Getpid(), counter.Add(1))
	if err := exec(ctx, "postgres", fmt.Sprintf("create database %s template %s", name, template)); err != nil {
		t.Fatalf("dbtest: create database: %v", err)
	}
	d, err := db.Open(ctx, URL(name))
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

func start() {
	tmpDir, startErr = os.MkdirTemp("", "tramo-pg-")
	if startErr != nil {
		return
	}
	first, last, err := portRange()
	if err != nil {
		startErr = err
		return
	}
	var lastErr error
	for p := first; p <= last; p++ {
		dir := filepath.Join(tmpDir, fmt.Sprint(p))
		pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V17).
			Port(p).
			Username("postgres").
			Password("postgres").
			Database("postgres").
			RuntimePath(filepath.Join(dir, "runtime")).
			DataPath(filepath.Join(dir, "data")).
			StartTimeout(60 * time.Second).
			Logger(io.Discard))
		if err := pg.Start(); err != nil {
			lastErr = err
			continue
		}
		server, port = pg, p
		break
	}
	if server == nil {
		startErr = fmt.Errorf("no free port in %d-%d: %w", first, last, lastErr)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if startErr = exec(ctx, "postgres", "create database "+template); startErr != nil {
		return
	}
	d, err := db.Open(ctx, URL(template))
	if err != nil {
		startErr = err
		return
	}
	startErr = d.Migrate(ctx)
	d.Close() // a template must have no open connections to be cloned
}

// portRange reads TRAMO_TEST_PG_PORTS ("first-last") so concurrent suites
// (backend, datos, qa) can run side by side; the default is backend's range.
func portRange() (uint32, uint32, error) {
	v := os.Getenv("TRAMO_TEST_PG_PORTS")
	if v == "" {
		return firstPort, lastPort, nil
	}
	var a, b uint32
	if _, err := fmt.Sscanf(v, "%d-%d", &a, &b); err != nil || a == 0 || b < a || b > 65535 {
		return 0, 0, fmt.Errorf("TRAMO_TEST_PG_PORTS=%q, want first-last like 54349-54358", v)
	}
	return a, b, nil
}

func exec(ctx context.Context, database, sql string) error {
	conn, err := pgx.Connect(ctx, URL(database))
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}
