package migrations

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var versionFile = regexp.MustCompile(`^(\d{5})_[a-z0-9_]+\.(sql|go)$`)

// goose refuses to start a database that already applied a higher version
// than a migration it has not run ("missing (out-of-order) migration").
// So one sequence: a migration that is not in main yet must take a number
// above the highest one in main. main is origin/main (MIGRATIONS_BASE
// overrides it). A branch that adds 00008 while main is at 00109 fails.
//
// Without the base ref the test skips locally and fails in CI, so it never
// passes there by not looking (CI checks out with fetch-depth: 0).
func TestMigrationsAboveMain(t *testing.T) {
	base := os.Getenv("MIGRATIONS_BASE")
	if base == "" {
		base = "origin/main"
	}
	listing, err := exec.Command("git", "ls-tree", "--name-only", base, "--", ".").Output()
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("CI without %s: fetch it (fetch-depth: 0) so migrations can be checked against main: %v", base, err)
		}
		t.Skipf("no %s here: %v", base, err)
	}
	inMain := map[string]bool{}
	highest, highestName := 0, ""
	for _, name := range strings.Fields(string(listing)) {
		m := versionFile.FindStringSubmatch(name)
		if m == nil || strings.HasSuffix(name, "_test.go") {
			continue
		}
		inMain[name] = true
		if v, _ := strconv.Atoi(m[1]); v > highest {
			highest, highestName = v, name
		}
	}
	if highest == 0 {
		t.Fatalf("no migrations in %s: the check is not looking", base)
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, e := range entries {
		m := versionFile.FindStringSubmatch(e.Name())
		if m == nil || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if other, dup := seen[v]; dup {
			t.Errorf("%s and %s share version %05d", other, e.Name(), v)
		}
		seen[v] = e.Name()
		if !inMain[e.Name()] && v <= highest {
			t.Errorf("%s is not in %s and its version is not above %s: a database already at %05d refuses it; take the next number above the highest",
				e.Name(), base, highestName, highest)
		}
	}
}
