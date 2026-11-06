package audit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

// requestCtx returns a context as a real request would carry it.
func requestCtx(t *testing.T, s db.Scope) context.Context {
	t.Helper()
	var ctx context.Context
	h := httpx.RequestMeta(nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.10:4000"
	r.Header.Set("X-Request-Id", "req-1")
	h.ServeHTTP(httptest.NewRecorder(), r)
	return db.WithScope(ctx, s)
}

func TestRecordFillsWhoWhereAndStaysInTenant(t *testing.T) {
	d := dbtest.New(t)
	bg := context.Background()
	a := db.Scope{TenantID: uuid.New(), UserID: uuid.New()}
	b := db.Scope{TenantID: uuid.New(), UserID: uuid.New()}
	err := d.System(bg, func(tx pgx.Tx) error {
		for i, s := range []db.Scope{a, b} {
			if _, err := tx.Exec(bg, `insert into tenants (id, slug, name) values ($1, $2, 'x')`, s.TenantID, []string{"a", "b"}[i]); err != nil {
				return err
			}
			if _, err := tx.Exec(bg, `insert into users (id, email) values ($1, $2)`, s.UserID, []string{"a@x.test", "b@x.test"}[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	actx := requestCtx(t, a)
	err = d.InTenant(actx, func(tx pgx.Tx) error {
		return audit.Record(actx, tx, audit.Entry{Action: "matching.approve", Subject: "matches:1", Detail: map[string]any{"tray": "exception"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	// Writing an entry for another tenant from a tenant transaction fails.
	err = d.InTenant(actx, func(tx pgx.Tx) error {
		return audit.Record(actx, tx, audit.Entry{Action: "x.y", Subject: "t:1", TenantID: b.TenantID})
	})
	if err == nil {
		t.Fatal("tenant a wrote an audit row for tenant b")
	}

	var tenant, user uuid.UUID
	var action, reqID, ip, tray string
	err = d.System(bg, func(tx pgx.Tx) error {
		return tx.QueryRow(bg, `select tenant_id, user_id, action, request_id, host(ip), detail->>'tray' from audit_log`).
			Scan(&tenant, &user, &action, &reqID, &ip, &tray)
	})
	if err != nil {
		t.Fatal(err)
	}
	if tenant != a.TenantID || user != a.UserID || action != "matching.approve" || reqID != "req-1" || ip != "192.0.2.10" || tray != "exception" {
		t.Fatalf("row: %v %v %s %s %s %s", tenant, user, action, reqID, ip, tray)
	}

	// Tenant b does not see tenant a's trail.
	bctx := requestCtx(t, b)
	err = d.InTenant(bctx, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(bctx, `select count(*) from audit_log`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("tenant b sees %d audit rows", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The audit trail stores the IP resolved by httpx.RequestMeta: the TCP peer
// unless it is a trusted proxy, then the last X-Forwarded-For hop. A
// spoofed header from an untrusted peer never reaches audit_log.ip.
func TestAuditStoresTheResolvedIP(t *testing.T) {
	d := dbtest.New(t)
	bg := context.Background()
	s := db.Scope{TenantID: uuid.New(), UserID: uuid.New()}
	err := d.System(bg, func(tx pgx.Tx) error {
		if _, err := tx.Exec(bg, `insert into tenants (id, slug, name) values ($1, 'a', 'x')`, s.TenantID); err != nil {
			return err
		}
		_, err := tx.Exec(bg, `insert into users (id, email) values ($1, 'a@x.test')`, s.UserID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	proxies := []netip.Prefix{netip.MustParsePrefix("172.30.0.10/32")}
	record := func(peer, xff, subject string) {
		var ctx context.Context
		h := httpx.RequestMeta(proxies)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() }))
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", xff)
		h.ServeHTTP(httptest.NewRecorder(), r)
		ctx = db.WithScope(ctx, s)
		if err := d.InTenant(ctx, func(tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Entry{Action: "test.ip", Subject: subject})
		}); err != nil {
			t.Fatal(err)
		}
	}
	record("203.0.113.7:5000", "198.51.100.66", "untrusted")
	record("172.30.0.10:5000", "6.6.6.6, 198.51.100.1", "trusted")

	for subject, want := range map[string]string{"untrusted": "203.0.113.7", "trusted": "198.51.100.1"} {
		var ip string
		err := d.System(bg, func(tx pgx.Tx) error {
			return tx.QueryRow(bg, `select host(ip) from audit_log where subject = $1`, subject).Scan(&ip)
		})
		if err != nil {
			t.Fatal(err)
		}
		if ip != want {
			t.Errorf("%s peer: audit_log.ip = %s, want %s", subject, ip, want)
		}
	}
}

func TestRecordRequiresActionAndSubject(t *testing.T) {
	if err := audit.Record(context.Background(), nil, audit.Entry{}); err == nil {
		t.Fatal("accepted an empty entry")
	}
}
