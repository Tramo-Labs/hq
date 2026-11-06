package db_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/migrations"
)

// The case qa hit: a database deployed from main has every migration of
// main applied. It must take this branch's new ones; goose refuses any
// migration numbered below the database version. So: migrate an empty
// database with main's migrations only (origin/main, or MIGRATIONS_BASE),
// then with everything, as an upgrade would. Skips locally without the ref,
// fails in CI (fetch-depth: 0).
func TestDatabaseFromMainUpgrades(t *testing.T) {
	base := os.Getenv("MIGRATIONS_BASE")
	if base == "" {
		base = "origin/main"
	}
	dir := filepath.Join("..", "..", "..", "migrations")
	listing, err := exec.Command("git", "-C", dir, "ls-tree", "--name-only", base, "--", ".").Output()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI without %s: %v", base, err)
		}
		t.Skipf("no %s here: %v", base, err)
	}
	inMain := fstest.MapFS{}
	for _, name := range strings.Fields(string(listing)) {
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		b, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatalf("%s is in %s but not embedded here: %v", name, base, err)
		}
		inMain[name] = &fstest.MapFile{Data: b}
	}
	if len(inMain) == 0 {
		t.Fatalf("no migrations in %s: the check is not looking", base)
	}

	ctx := context.Background()
	d := dbtest.NewEmpty(t)
	sqlDB := stdlib.OpenDBFromPool(d.Pool)
	defer func() { _ = sqlDB.Close() }()
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, inMain, goose.WithGoMigrations(migrations.Go...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("main's migrations: %v", err)
	}
	at, err := p.GetDBVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("a database from main (at %05d) does not take this branch's migrations: %v", at, err)
	}
}
