package idempotency_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/idempotency"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

type harness struct {
	t     *testing.T
	d     *db.DB
	calls atomic.Int32
	h     http.Handler
	// status the handler answers with; block, if set, is waited on.
	status int
	block  chan struct{}
	inside chan struct{}
}

func newHarness(t *testing.T) *harness {
	hs := &harness{t: t, d: dbtest.New(t), status: http.StatusCreated}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hs.calls.Add(1)
		if hs.inside != nil {
			hs.inside <- struct{}{}
		}
		if hs.block != nil {
			<-hs.block
		}
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(hs.status)
		fmt.Fprintf(w, `{"call":%d,"echo":%q}`, n, b)
	})
	hs.h = idempotency.Middleware(hs.d)(inner)
	return hs
}

func (hs *harness) post(ctx context.Context, key, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/things", strings.NewReader(body)).WithContext(ctx)
	if key != "" {
		r.Header.Set(idempotency.Header, key)
	}
	w := httptest.NewRecorder()
	hs.h.ServeHTTP(w, r)
	return w
}

func TestReplaySameRequest(t *testing.T) {
	hs := newHarness(t)
	key := uuid.NewString()
	first := hs.post(context.Background(), key, `{"a":1}`)
	second := hs.post(context.Background(), key, `{"a":1}`)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("codes %d, %d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay differs: %s vs %s", first.Body, second.Body)
	}
	if second.Header().Get("Idempotent-Replayed") != "true" || second.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("replay headers: %v", second.Header())
	}
	if hs.calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", hs.calls.Load())
	}
}

func TestSameKeyOtherBodyIs422(t *testing.T) {
	hs := newHarness(t)
	key := uuid.NewString()
	hs.post(context.Background(), key, `{"a":1}`)
	w := hs.post(context.Background(), key, `{"a":2}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "idempotency_key_reused") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if hs.calls.Load() != 1 {
		t.Fatalf("handler ran %d times", hs.calls.Load())
	}
}

func TestConcurrentSameKeyIs409(t *testing.T) {
	hs := newHarness(t)
	hs.block, hs.inside = make(chan struct{}), make(chan struct{}, 1)
	key := uuid.NewString()
	var wg sync.WaitGroup
	var first *httptest.ResponseRecorder
	wg.Add(1)
	go func() { defer wg.Done(); first = hs.post(context.Background(), key, `{}`) }()
	<-hs.inside // the first request holds the key
	second := hs.post(context.Background(), key, `{}`)
	close(hs.block)
	wg.Wait()
	if second.Code != http.StatusConflict {
		t.Fatalf("concurrent duplicate got %d %s", second.Code, second.Body)
	}
	if first.Code != http.StatusCreated || hs.calls.Load() != 1 {
		t.Fatalf("first %d, calls %d", first.Code, hs.calls.Load())
	}
}

func TestServerErrorReleasesKey(t *testing.T) {
	hs := newHarness(t)
	hs.status = http.StatusInternalServerError
	key := uuid.NewString()
	hs.post(context.Background(), key, `{}`)
	hs.status = http.StatusCreated
	w := hs.post(context.Background(), key, `{}`)
	if w.Code != http.StatusCreated || hs.calls.Load() != 2 {
		t.Fatalf("retry after 500: %d, calls %d", w.Code, hs.calls.Load())
	}
}

func TestClientErrorIsStored(t *testing.T) {
	hs := newHarness(t)
	hs.status = http.StatusBadRequest
	key := uuid.NewString()
	hs.post(context.Background(), key, `{}`)
	hs.status = http.StatusCreated
	if w := hs.post(context.Background(), key, `{}`); w.Code != http.StatusBadRequest || hs.calls.Load() != 1 {
		t.Fatalf("4xx not replayed: %d, calls %d", w.Code, hs.calls.Load())
	}
}

func TestKeysAreScopedByTenant(t *testing.T) {
	hs := newHarness(t)
	a, b := uuid.New(), uuid.New()
	err := hs.d.System(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `insert into tenants (id, slug, name) values ($1, 'a', 'a'), ($2, 'b', 'b')`, a, b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	hs.post(db.WithScope(context.Background(), db.Scope{TenantID: a}), key, `{}`)
	w := hs.post(db.WithScope(context.Background(), db.Scope{TenantID: b}), key, `{}`)
	if w.Header().Get("Idempotent-Replayed") != "" || hs.calls.Load() != 2 {
		t.Fatalf("tenant b got tenant a's replay (calls %d)", hs.calls.Load())
	}
}

func TestExpiredKeyRunsAgain(t *testing.T) {
	hs := newHarness(t)
	key := uuid.NewString()
	hs.post(context.Background(), key, `{"a":1}`)
	err := hs.d.System(context.Background(), func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `update idempotency_requests set created_at = now() - interval '25 hours'`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if w := hs.post(context.Background(), key, `{"a":2}`); w.Code != http.StatusCreated || hs.calls.Load() != 2 {
		t.Fatalf("expired key: %d, calls %d", w.Code, hs.calls.Load())
	}
	n, err := idempotency.Purge(context.Background(), hs.d)
	if err != nil || n != 0 {
		t.Fatalf("purge removed %d fresh keys, %v", n, err)
	}
}

func TestBadKeyAndNoKey(t *testing.T) {
	hs := newHarness(t)
	if w := hs.post(context.Background(), "not-a-uuid", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad key: %d", w.Code)
	}
	hs.post(context.Background(), "", `{}`)
	hs.post(context.Background(), "", `{}`)
	if hs.calls.Load() != 2 {
		t.Fatalf("requests without key must pass through, calls %d", hs.calls.Load())
	}
}
