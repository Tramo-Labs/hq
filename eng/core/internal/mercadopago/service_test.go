package mercadopago_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago"
	"github.com/Tramo-Labs/hq/eng/core/internal/mercadopago/mpfake"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/crypto"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const seller = 241983636

type env struct {
	db                                *db.DB
	kr                                *crypto.Keyring
	fake                              *mpfake.Fake
	svc                               *mercadopago.Service
	clock                             *clock
	tenantA, tenantB                  uuid.UUID
	owner, owner2, accountant, viewer uuid.UUID
	ownerB                            uuid.UUID
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

func newEnv(t *testing.T) env {
	t.Helper()
	master := make([]byte, 32)
	_, _ = rand.Read(master)
	kr, err := crypto.NewKeyring(master)
	if err != nil {
		t.Fatal(err)
	}
	e := env{db: dbtest.New(t), kr: kr, fake: mpfake.New(), clock: &clock{t: time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)},
		tenantA: uuid.New(), tenantB: uuid.New(), owner: uuid.New(), owner2: uuid.New(), accountant: uuid.New(),
		viewer: uuid.New(), ownerB: uuid.New()}
	t.Cleanup(e.fake.Close)
	e.fake.Now = e.clock.now
	ctx := context.Background()
	err = pgx.BeginFunc(ctx, e.db.Pool, func(tx pgx.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`insert into tenants (id, slug, name) values ($1, 'molino-a', 'A'), ($2, 'molino-b', 'B')`, []any{e.tenantA, e.tenantB}},
			{`insert into users (id, email) values ($1, 'o@t.test'), ($2, 'o2@t.test'), ($3, 'a@t.test'), ($4, 'v@t.test'), ($5, 'ob@t.test')`,
				[]any{e.owner, e.owner2, e.accountant, e.viewer, e.ownerB}},
			{`insert into memberships (user_id, tenant_id, role) values ($1, $6, 'owner'), ($2, $6, 'owner'), ($3, $6, 'accountant'),
				($4, $6, 'viewer'), ($5, $7, 'owner')`, []any{e.owner, e.owner2, e.accountant, e.viewer, e.ownerB, e.tenantA, e.tenantB}},
		} {
			if _, err := tx.Exec(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		for _, id := range []uuid.UUID{e.tenantA, e.tenantB} {
			if err := kr.EnsureTenantKey(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = &mercadopago.Service{DB: e.db, Keyring: kr, Now: e.clock.now, HTTP: e.fake.Client(),
		Config: mercadopago.Config{ClientID: e.fake.ClientID, ClientSecret: e.fake.ClientSecret,
			RedirectURI: e.fake.RedirectURI, AuthURL: "https://auth.example.test/authorization", APIURL: e.fake.URL}}
	return e
}

func as(tenant, user uuid.UUID) context.Context {
	return db.WithScope(context.Background(), db.Scope{TenantID: tenant, UserID: user})
}

func job(tenant uuid.UUID) context.Context {
	return db.WithScope(context.Background(), db.Scope{TenantID: tenant})
}

// authorize starts a connection and plays the seller granting it: it
// returns the code and state Mercado Pago puts in the return URL.
func (e env) authorize(t *testing.T, ctx context.Context) (code, state string) {
	t.Helper()
	u, exp, err := e.svc.Authorize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(e.clock.now().Add(mercadopago.StateTTL)) {
		t.Fatalf("state expires %s", exp)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	if parsed.Host != "auth.example.test" || q.Get("response_type") != "code" || q.Get("state") == "" {
		t.Fatalf("authorize URL: %s", u)
	}
	code, ok := e.fake.Grant(q.Get("client_id"), q.Get("redirect_uri"), q.Get("code_challenge"), q.Get("code_challenge_method"), seller)
	if !ok {
		t.Fatalf("Mercado Pago would refuse this authorization URL: %s", u)
	}
	return code, q.Get("state")
}

func (e env) rawTokens(t *testing.T) (access, refresh []byte, status string) {
	t.Helper()
	err := e.db.Pool.QueryRow(context.Background(), `select access_token, refresh_token, status from mercadopago.connections where tenant_id = $1`,
		e.tenantA).Scan(&access, &refresh, &status)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func (e env) refreshToken(t *testing.T) string {
	t.Helper()
	_, sealed, _ := e.rawTokens(t)
	var out string
	err := pgx.BeginFunc(context.Background(), e.db.Pool, func(tx pgx.Tx) error {
		b, err := e.kr.Decrypt(context.Background(), tx, e.tenantA, sealed, "mercadopago.token")
		out = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func count(t *testing.T, e env, sql string) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConnectStoresSealedTokens(t *testing.T) {
	e := newEnv(t)
	code, state := e.authorize(t, as(e.tenantA, e.owner))
	st, err := e.svc.Connect(as(e.tenantA, e.owner), code, state)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != mercadopago.Connected || st.MPUserID != "241983636" || st.TokenExpiresAt == nil ||
		!st.TokenExpiresAt.Equal(e.clock.now().Add(180*24*time.Hour)) || *st.ConnectedBy != e.owner {
		t.Fatalf("status: %+v", st)
	}
	access, refresh, _ := e.rawTokens(t)
	for _, b := range [][]byte{access, refresh} {
		if len(b) == 0 || bytes.Contains(b, []byte("APP_USR-")) || bytes.Contains(b, []byte("TG-")) {
			t.Fatalf("a token is stored in clear: %q", b)
		}
	}
	if got, err := e.svc.Get(as(e.tenantA, e.accountant)); err != nil || got.Status != mercadopago.Connected {
		t.Fatalf("accountant sees: %+v %v", got, err)
	}
	if _, err := e.svc.Get(as(e.tenantA, e.viewer)); !errors.Is(err, mercadopago.ErrForbidden) {
		t.Fatalf("viewer sees the connection: %v", err)
	}
	if got, _ := e.svc.Get(as(e.tenantB, e.ownerB)); got.Status != mercadopago.Disconnected {
		t.Fatalf("tenant B sees A's connection: %+v", got)
	}
	if n := count(t, e, `select count(*) from audit_log where action = 'mercadopago.connect'`); n != 1 {
		t.Fatalf("%d connect audit rows", n)
	}
}

// Every refusal of connect stores nothing, and each has its own error.
func TestConnectRefusals(t *testing.T) {
	e := newEnv(t)
	own := as(e.tenantA, e.owner)

	if _, err := e.svc.Connect(own, "TG-x", "nope"); !errors.Is(err, mercadopago.ErrStateInvalid) {
		t.Errorf("unknown state: %v", err)
	}
	code, state := e.authorize(t, own)
	if _, err := e.svc.Connect(as(e.tenantA, e.owner2), code, state); !errors.Is(err, mercadopago.ErrStateNotYours) {
		t.Errorf("another owner finishing it: %v", err)
	}
	if _, err := e.svc.Connect(as(e.tenantB, e.ownerB), code, state); !errors.Is(err, mercadopago.ErrStateInvalid) {
		t.Errorf("another tenant finishing it: %v", err)
	}
	if _, err := e.svc.Connect(own, "TG-wrong", state); !errors.Is(err, mercadopago.ErrCodeRejected) {
		t.Errorf("a code Mercado Pago never gave: %v", err)
	}
	e.fake.FailNext(500)
	if _, err := e.svc.Connect(own, code, state); !errors.Is(err, mercadopago.ErrMPUnavailable) {
		t.Errorf("Mercado Pago down: %v", err)
	}
	if n := count(t, e, `select count(*) from mercadopago.connections`); n != 0 {
		t.Fatalf("refusals stored %d connections", n)
	}
	// None of the refusals spent the state: the right code still connects once.
	if _, err := e.svc.Connect(own, code, state); err != nil {
		t.Fatalf("connect after the refusals: %v", err)
	}
	if _, err := e.svc.Connect(own, code, state); !errors.Is(err, mercadopago.ErrStateUsed) {
		t.Errorf("same state twice: %v", err)
	}
	code2, state2 := e.authorize(t, own)
	e.clock.add(mercadopago.StateTTL)
	if _, err := e.svc.Connect(own, code2, state2); !errors.Is(err, mercadopago.ErrStateExpired) {
		t.Errorf("state after ten minutes: %v", err)
	}
}

// Only the owner connects or disconnects; without an app nothing does, but
// the role is checked first.
func TestRolesAndConfiguration(t *testing.T) {
	e := newEnv(t)
	for _, u := range []uuid.UUID{e.accountant, e.viewer} {
		if _, _, err := e.svc.Authorize(as(e.tenantA, u)); !errors.Is(err, mercadopago.ErrForbidden) {
			t.Errorf("authorize by %s: %v", u, err)
		}
		if _, err := e.svc.Disconnect(as(e.tenantA, u)); !errors.Is(err, mercadopago.ErrForbidden) {
			t.Errorf("disconnect by %s: %v", u, err)
		}
	}
	e.svc.Config = mercadopago.Config{}
	if _, _, err := e.svc.Authorize(as(e.tenantA, e.viewer)); !errors.Is(err, mercadopago.ErrForbidden) {
		t.Errorf("viewer without an app: %v, want forbidden first", err)
	}
	if _, _, err := e.svc.Authorize(as(e.tenantA, e.owner)); !errors.Is(err, mercadopago.ErrNotConfigured) {
		t.Errorf("owner without an app: %v", err)
	}
	if st, _ := e.svc.Get(as(e.tenantA, e.owner)); st.Configured {
		t.Error("configured without an app")
	}
}

func connect(t *testing.T, e env) {
	t.Helper()
	code, state := e.authorize(t, as(e.tenantA, e.owner))
	if _, err := e.svc.Connect(as(e.tenantA, e.owner), code, state); err != nil {
		t.Fatal(err)
	}
}

// Tokens far from expiry are left alone; close to it they are renewed and
// the refresh token rotates.
func TestRefreshRenewsAndRotates(t *testing.T) {
	e := newEnv(t)
	connect(t, e)
	before := e.fake.Requests()
	if renewed, err := e.svc.Refresh(job(e.tenantA)); err != nil || renewed || e.fake.Requests() != before {
		t.Fatalf("renewed a token 180 days from expiry: %v %v", renewed, err)
	}
	old := e.refreshToken(t)
	e.clock.add(180*24*time.Hour - mercadopago.RefreshBefore + time.Hour)
	if renewed, err := e.svc.Refresh(job(e.tenantA)); err != nil || !renewed {
		t.Fatalf("not renewed within the window: %v %v", renewed, err)
	}
	if e.refreshToken(t) == old {
		t.Fatal("refresh token not replaced by the rotated one")
	}
	st, _ := e.svc.Get(as(e.tenantA, e.owner))
	if st.LastRefreshAt == nil || !st.TokenExpiresAt.Equal(e.clock.now().Add(180*24*time.Hour)) {
		t.Fatalf("after renewal: %+v", st)
	}
	if renewed, _ := e.svc.Refresh(job(e.tenantB)); renewed {
		t.Fatal("renewed a tenant without a connection")
	}
}

// Four renewals at once make one call to Mercado Pago: the others wait for
// the first and find the token renewed.
func TestConcurrentRefreshCallsMercadoPagoOnce(t *testing.T) {
	e := newEnv(t)
	connect(t, e)
	e.clock.add(170 * 24 * time.Hour)
	before := e.fake.Requests()
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := e.svc.Refresh(job(e.tenantA)); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := e.fake.Requests() - before; n != 1 {
		t.Fatalf("%d calls to Mercado Pago, want 1", n)
	}
	if _, _, status := e.rawTokens(t); status != mercadopago.Connected {
		t.Fatalf("status %s", status)
	}
}

// The seller removes the app in Mercado Pago: the next renewal gets
// invalid_grant, the tokens are deleted and the owner must reconnect. A
// Mercado Pago outage keeps the connection and is retried.
func TestRevokedInMercadoPagoAndOutages(t *testing.T) {
	e := newEnv(t)
	connect(t, e)
	e.clock.add(170 * 24 * time.Hour)
	e.fake.FailNext(503)
	if renewed, err := e.svc.Refresh(job(e.tenantA)); err != nil || renewed {
		t.Fatalf("outage: %v %v", renewed, err)
	}
	st, _ := e.svc.Get(as(e.tenantA, e.owner))
	if st.Status != mercadopago.Connected || st.LastErrorCode != mercadopago.CodeUnavailable {
		t.Fatalf("after an outage: %+v", st)
	}
	e.fake.Revoke(seller)
	if _, err := e.svc.Refresh(job(e.tenantA)); err != nil {
		t.Fatal(err)
	}
	st, _ = e.svc.Get(as(e.tenantA, e.owner))
	if st.Status != mercadopago.NeedsReconnect || st.LastErrorCode != mercadopago.CodeRevoked {
		t.Fatalf("after the seller revoked: %+v", st)
	}
	if access, refresh, _ := e.rawTokens(t); access != nil || refresh != nil {
		t.Fatal("tokens kept after Mercado Pago revoked them")
	}
	before := e.fake.Requests()
	if _, err := e.svc.Refresh(job(e.tenantA)); err != nil || e.fake.Requests() != before {
		t.Fatalf("kept calling Mercado Pago after a revocation: %v", err)
	}
}

// A token without a refresh token (an app without offline_access) cannot
// be renewed; once it expires the owner reconnects.
func TestNoRefreshTokenExpires(t *testing.T) {
	e := newEnv(t)
	e.fake.NoRefresh = true
	connect(t, e)
	e.clock.add(170 * 24 * time.Hour)
	if _, err := e.svc.Refresh(job(e.tenantA)); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Get(as(e.tenantA, e.owner)); st.Status != mercadopago.Connected {
		t.Fatalf("before expiry: %+v", st)
	}
	e.clock.add(11 * 24 * time.Hour)
	if _, err := e.svc.Refresh(job(e.tenantA)); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.svc.Get(as(e.tenantA, e.owner)); st.Status != mercadopago.NeedsReconnect {
		t.Fatalf("after expiry: %+v", st)
	}
}

func TestDisconnect(t *testing.T) {
	e := newEnv(t)
	connect(t, e)
	st, err := e.svc.Disconnect(as(e.tenantA, e.owner))
	if err != nil || st.Status != mercadopago.Disconnected || !st.RevokeInMP {
		t.Fatalf("disconnect: %+v %v", st, err)
	}
	if n := count(t, e, `select count(*) from mercadopago.connections`); n != 0 {
		t.Fatalf("%d connections left", n)
	}
	if st, _ := e.svc.Disconnect(as(e.tenantA, e.owner)); st.RevokeInMP {
		t.Fatal("asked to revoke in Mercado Pago with nothing connected")
	}
}
