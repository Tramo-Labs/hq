package dbtest_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// The child tests run only inside the subprocesses started below.
const childEnv = "TRAMO_DBTEST_CHILD"

func TestChildFails(t *testing.T) {
	if os.Getenv(childEnv) != "fail" {
		t.Skip("child process only")
	}
	dbtest.New(t)
	t.Fatal("failing on purpose")
}

func TestChildPanics(t *testing.T) {
	if os.Getenv(childEnv) != "panic" {
		t.Skip("child process only")
	}
	dbtest.New(t)
	panic("panicking on purpose")
}

func TestChildPasses(t *testing.T) {
	if os.Getenv(childEnv) != "pass" {
		t.Skip("child process only")
	}
	dbtest.New(t)
}

// child runs this test binary with only the named child test, its
// temporary files under root, and reports whether it passed.
func child(t *testing.T, root, mode, test string) bool {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$", "-test.count=1") // #nosec G204 -- this test binary
	cmd.Env = append(os.Environ(), childEnv+"="+mode, "TMPDIR="+root)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("%s: %v\n%s", mode, err, out)
	}
	return err == nil
}

func leftovers(t *testing.T, root string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(root, "tramo-pg-*"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// postmasters lists the pids in the postmaster.pid files under dirs.
func postmasters(t *testing.T, dirs []string) []int {
	t.Helper()
	var pids []int
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*", "data", "postmaster.pid"))
		for _, f := range files {
			b, err := os.ReadFile(f) // #nosec G304 -- under this test's own directory
			if err != nil {
				continue
			}
			if pid, err := strconv.Atoi(strings.SplitN(string(b), "\n", 2)[0]); err == nil {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// The embedded Postgres directory (about 200 MB) never outlives the test
// binary that made it: a failing run removes it, and a run that could not
// (a panic, a timeout, a kill) leaves it for the next run, which stops its
// postmaster and removes it. Without this /tmp fills up and every session
// on the machine fails.
func TestEmbeddedPostgresDirectoryIsRemoved(t *testing.T) {
	if testing.Short() {
		t.Skip("starts embedded postgres; skipped with -short")
	}
	if os.Getenv(childEnv) != "" {
		t.Skip("parent only")
	}
	root := t.TempDir()

	if child(t, root, "fail", "TestChildFails") {
		t.Fatal("the failing child passed")
	}
	if l := leftovers(t, root); len(l) != 0 {
		t.Fatalf("a failing run left %v", l)
	}

	if child(t, root, "panic", "TestChildPanics") {
		t.Fatal("the panicking child passed")
	}
	left := leftovers(t, root)
	pids := postmasters(t, left)
	t.Cleanup(func() { // if the sweep fails, do not leave it running
		for _, pid := range pids {
			if alive(pid) {
				_ = syscall.Kill(pid, syscall.SIGINT)
			}
		}
	})
	if len(left) != 1 || len(pids) != 1 || !alive(pids[0]) {
		t.Fatalf("expected one leftover with a running postmaster after a panic: %v %v", left, pids)
	}

	if !child(t, root, "pass", "TestChildPasses") {
		t.Fatal("the passing child failed")
	}
	if l := leftovers(t, root); len(l) != 0 {
		t.Fatalf("the next run did not sweep the leftover: %v", l)
	}
	if logs, _ := filepath.Glob(filepath.Join(root, "embedded_postgres_log*")); len(logs) != 0 {
		t.Fatalf("server logs left behind: %v", logs)
	}
	if alive(pids[0]) {
		t.Fatalf("the leftover's postmaster %d still runs", pids[0])
	}
}
