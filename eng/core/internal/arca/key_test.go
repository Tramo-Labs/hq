package arca

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The ARCA certificate and private key exist only in the arca-gateway
// process. No other package may even read the variables that carry them.
func TestOnlyTheGatewayReadsTheKey(t *testing.T) {
	root := filepath.Join("..", "..") // eng/core
	allowed := []string{"internal/arca/", "cmd/arca-gateway/"}
	names := []string{"ARCA_KEY", "ARCA_CERT"}
	seen := map[string]int{}
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
		inAllowed := false
		for _, a := range allowed {
			if strings.HasPrefix(rel, a) {
				inAllowed = true
			}
		}
		for _, name := range names {
			if !strings.Contains(string(b), name) {
				continue
			}
			seen[name]++
			if !inAllowed {
				t.Errorf("%s mentions %s; only %v may", rel, name, allowed)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if seen[name] == 0 {
			t.Fatalf("no file mentions %s: the scan is broken", name)
		}
	}
}
