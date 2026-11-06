package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A new outbox is created 0600 in the same call that creates it, never
// world readable for an instant and never relying on a later chmod. The
// umask is opened wide so only the creation mode decides.
func TestNewOutboxIsCreatedPrivate(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "outbox")
	if err := (&Outbox{Path: path}).Send(context.Background(), Message{To: "a@x.test", Subject: "hi", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("new outbox mode %v %v, want 0600", st.Mode().Perm(), err)
	}
}

func TestOutboxIsPrivateAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox")
	// A file left world readable is tightened on the next write.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	o := &Outbox{Path: path}
	for _, to := range []string{"a@x.test", "b@x.test"} {
		if err := o.Send(context.Background(), Message{To: to, Subject: "hi", Body: "body " + to}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode().Perm(), err)
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "From tramo ") != 2 || !strings.Contains(string(b), "body b@x.test") {
		t.Fatalf("outbox:\n%s", b)
	}
	if err := o.Send(context.Background(), Message{To: "a@x.test\nBcc: c@x.test", Subject: "x"}); err == nil {
		t.Fatal("header injection accepted")
	}
}
