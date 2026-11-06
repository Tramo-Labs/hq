package db_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// systemCallers are the only packages allowed to open a System transaction
// (tramo_system, cross tenant). Adding one is a design decision: it needs
// a line in ADR 0002 and grants in a migration.
var systemCallers = map[string]string{
	"internal/platform/db":          "definition",
	"internal/platform/idempotency": "keys are claimed before the tenant transaction; registration has no tenant",
	"internal/identity":             "registration, login and refresh happen before a tenant scope exists",
}

// TestSystemCallersAreAllowlisted fails when production code outside the
// allowlist calls .System(ctx, fn). Tests may use it to seed data.
func TestSystemCallersAreAllowlisted(t *testing.T) {
	root := filepath.Join("..", "..", "..") // eng/core
	fset := token.NewFileSet()
	found := 0
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if e.IsDir() {
			if rel != "." && (rel == "gen" || rel == "bin" || strings.HasPrefix(e.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		// Any selector named System counts: a call (d.System(ctx, fn)), a
		// method value (f := d.System) or a method expression
		// ((*db.DB).System). Matching by name, not by type, is stricter on
		// purpose: an unrelated System elsewhere has to be renamed or listed.
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "System" {
				return true
			}
			found++
			dir := filepath.ToSlash(filepath.Dir(rel))
			if _, ok := systemCallers[dir]; !ok {
				t.Errorf("%s: references System; only %v may (use InTenant, or add a justified entry and ADR 0002 line)",
					fset.Position(sel.Pos()), keys(systemCallers))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("found no System calls at all: the scan is broken")
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
