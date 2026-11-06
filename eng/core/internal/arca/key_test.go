package arca

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ARCA private key exists only in the arca-gateway process. No other
// package may even read the variable that carries it.
func TestOnlyTheGatewayReadsTheKey(t *testing.T) {
	root := filepath.Join("..", "..") // eng/core
	allowed := []string{"internal/arca/", "cmd/arca-gateway/"}
	seen := 0
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			if rel != "." && (rel == "gen" || rel == "bin" || strings.HasPrefix(e.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(b), "ARCA_KEY") {
			return nil
		}
		seen++
		for _, a := range allowed {
			if strings.HasPrefix(rel, a) {
				return nil
			}
		}
		t.Errorf("%s mentions ARCA_KEY; only %v may", rel, allowed)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no file mentions ARCA_KEY: the scan is broken")
	}
}
