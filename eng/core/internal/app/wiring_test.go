package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Guard: every binary that builds app.Deps passes CORSOrigins. Without it
// the CORS list is empty, and so is the refresh Origin check (F22): every
// cookie refresh and logout from the portal is a 403. make dev shipped like
// that once. This reads the source of cmd/*, so a new binary is covered
// the moment it builds the api.
func TestEveryBinaryPassesTheBrowserOrigins(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Deps" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "app" {
				return true
			}
			found++
			for _, e := range lit.Elts {
				if kv, ok := e.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "CORSOrigins" {
						return true
					}
				}
			}
			t.Errorf("%s: app.Deps without CORSOrigins: cookie refresh and logout would all be 403", fset.Position(lit.Pos()))
			return true
		})
	}
	if found < 2 {
		t.Fatalf("found %d app.Deps literals in cmd/*, want at least api and dev: the guard is not looking", found)
	}
}
