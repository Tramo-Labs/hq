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
//
// Disk: the server lives in $TMPDIR/tramo-pg-* (about 200 MB), removed by
// Main when the tests end, passed or failed. A binary that ends without
// reaching it (a panic, a timeout, a kill) leaves it behind with its
// postmaster running; the next test binary to start sweeps every such
// directory whose owner process is gone: it stops the postmaster, then
// removes the directory. The Makefile points TMPDIR at the disk.
package dbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	// ownerFile, in each server directory, holds the pid of the test
	// binary that made it.
	ownerFile = "owner.pid"
	dirPrefix = "tramo-pg-"
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
// Main runs the tests, then stops the embedded server and removes its
// directory, whatever the result.
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

// sweep stops and removes the server directories under root left by test
// binaries that are gone. A directory without an owner file is not
// touched: it was not made by this code, or its owner is still writing it.
func sweep(root string) {
	dirs, _ := filepath.Glob(filepath.Join(root, dirPrefix+"*"))
	for _, dir := range dirs {
		b, err := os.ReadFile(filepath.Join(dir, ownerFile)) // #nosec G304 -- a dbtest directory
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || pid <= 0 || alive(pid) {
			continue
		}
		datas, _ := filepath.Glob(filepath.Join(dir, "*", "data", "postmaster.pid"))
		for _, pidFile := range datas {
			data := filepath.Dir(pidFile)
			pgctl := filepath.Join(filepath.Dir(data), "runtime", "bin", "pg_ctl")
			_ = osexec.Command(pgctl, "stop", "-D", data, "-m", "immediate", "-w").Run() // #nosec G204 -- pg_ctl inside a dbtest directory
		}
		_ = os.RemoveAll(dir)
	}
}

// alive reports whether a process with pid exists (EPERM: it exists, under
// another user).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// URL returns the connection string for a database on the test server.
func URL(name string) string { return URLAs(name, "postgres", "postgres") }

// URLAs is URL for another login role (test-only credentials).
func URLAs(name, user, password string) string {
	return fmt.Sprintf("postgres://%s:%s@localhost:%d/%s?sslmode=disable", user, password, port, name)
}

// DatabaseName returns the name of the database d is connected to.
func DatabaseName(t testing.TB, d *db.DB) string {
	t.Helper()
	var name string
	if err := d.Pool.QueryRow(context.Background(), "select current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	return name
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

// NewEmpty returns a pool on a fresh database with no migration applied, for
// tests that need to control what exists before migrating. Roles are per
// server, not per database: a test that alters them must restore them.
func NewEmpty(t testing.TB) *db.DB {
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
	name := fmt.Sprintf("e_%d_%d", os.Getpid(), counter.Add(1))
	if err := exec(ctx, "postgres", "create database "+name); err != nil {
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
	sweep(os.TempDir())
	tmpDir, startErr = os.MkdirTemp("", dirPrefix)
	if startErr != nil {
		return
	}
	if startErr = os.WriteFile(filepath.Join(tmpDir, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o600); startErr != nil {
		return
	}
	// embedded-postgres writes the server log to a temporary file it never
	// removes: point it inside tmpDir while starting, so it goes with it.
	// t.TempDir calls in the meantime land there too, and are removed by
	// their own test.
	if prev, had := os.LookupEnv("TMPDIR"); had {
		defer func() { _ = os.Setenv("TMPDIR", prev) }()
	} else {
		defer func() { _ = os.Unsetenv("TMPDIR") }()
	}
	if startErr = os.Setenv("TMPDIR", tmpDir); startErr != nil {
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
			// Each attempt extracts the whole runtime: remove it now, not
			// at the end, or ten busy ports cost ten copies.
			lastErr = err
			_ = os.RemoveAll(dir)
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
