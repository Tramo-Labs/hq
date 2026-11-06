package inbox_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/inbox"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

const domain = "casilla.test"

type env struct {
	db                        *db.DB
	tenantA, tenantB          uuid.UUID
	owner, accountant, viewer uuid.UUID
	ownerB                    uuid.UUID
}

func newEnv(t *testing.T) env {
	t.Helper()
	e := env{db: dbtest.New(t), tenantA: uuid.New(), tenantB: uuid.New(), owner: uuid.New(),
		accountant: uuid.New(), viewer: uuid.New(), ownerB: uuid.New()}
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`insert into tenants (id, slug, name) values ($1, 'molino-a', 'A'), ($2, 'molino-b', 'B')`, []any{e.tenantA, e.tenantB}},
		{`insert into users (id, email) values ($1, 'own@t.test'), ($2, 'acc@t.test'), ($3, 'vw@t.test'), ($4, 'ownb@t.test')`,
			[]any{e.owner, e.accountant, e.viewer, e.ownerB}},
		{`insert into memberships (user_id, tenant_id, role) values ($1, $4, 'owner'), ($2, $4, 'accountant'), ($3, $4, 'viewer'), ($5, $6, 'owner')`,
			[]any{e.owner, e.accountant, e.viewer, e.tenantA, e.ownerB, e.tenantB}},
	} {
		if _, err := e.db.Pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func as(tenant, user uuid.UUID) context.Context {
	return db.WithScope(context.Background(), db.Scope{TenantID: tenant, UserID: user})
}

// fakeIngest stands in for datos' ingestion without an account.
type fakeIngest struct {
	mu    sync.Mutex
	calls []inbox.Unassigned
}

func (f *fakeIngest) IngestUnassigned(_ context.Context, a inbox.Unassigned) (ledger.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, a)
	return ledger.Result{Status: ledger.Accepted, FileHash: "h", New: 3}, nil
}

func (f *fakeIngest) n() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// opened counts how many bytes the service read from attachments.
type opened struct{ bytes atomic.Int64 }

type countingReader struct {
	r io.Reader
	o *opened
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.o.bytes.Add(int64(n))
	return n, err
}

func (o *opened) att(name string, data []byte) inbox.Attachment {
	return inbox.Attachment{Filename: name, Open: func() (io.ReadCloser, error) {
		return io.NopCloser(countingReader{bytes.NewReader(data), o}), nil
	}}
}

// endless is an attachment of n bytes that is never in memory as a whole.
func (o *opened) endless(name string, n int64) inbox.Attachment {
	return inbox.Attachment{Filename: name, Open: func() (io.ReadCloser, error) {
		return io.NopCloser(countingReader{io.LimitReader(letters{}, n), o}), nil
	}}
}

type letters struct{}

func (letters) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

var (
	csv = []byte("fecha;importe\n01/11/2026;100,00\n")
	pdf = []byte("%PDF-1.7\n...")
)

type fixture struct {
	env
	svc    *inbox.Service
	ingest *fakeIngest
	to     string
}

func setup(t *testing.T, lim inbox.Limits) fixture {
	t.Helper()
	e := newEnv(t)
	f := fixture{env: e, ingest: &fakeIngest{}}
	f.svc = &inbox.Service{DB: e.db, Ingest: f.ingest, Domain: domain, Limits: lim,
		Now: func() time.Time { return time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC) }}
	mb, err := f.svc.Rotate(as(e.tenantA, e.owner))
	if err != nil {
		t.Fatal(err)
	}
	f.to = f.svc.Address(mb.Code)
	if _, err := f.svc.AddSender(as(e.tenantA, e.owner), "Banco@Example.com"); err != nil {
		t.Fatal(err)
	}
	return f
}

func msg(to, from string, atts ...inbox.Attachment) inbox.Message {
	return inbox.Message{To: to, From: from, MessageID: "<" + uuid.NewString() + "@mx>", SPFAligned: true, Attachments: atts}
}

func countRows(t *testing.T, e env, sql string) int {
	t.Helper()
	var n int
	if err := e.db.Pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The lookup role reaches one function and no table: checked against every
// table and function in the database, not a hand picked list, and by
// trying to read the tables that matter most.
func TestTheLookupRoleReadsNothingButTheFunction(t *testing.T) {
	f := setup(t, inbox.Limits{})
	ctx := context.Background()
	if n := countRows(t, f.env, `
		select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace
		 where c.relkind in ('r', 'p', 'v', 'm')
		   and n.nspname not in ('pg_catalog', 'information_schema')
		   and has_table_privilege('tramo_inbox', c.oid, 'select, insert, update, delete, truncate, references, trigger')`); n != 0 {
		t.Fatalf("tramo_inbox holds privileges on %d tables", n)
	}
	rows, err := f.db.Pool.Query(ctx, `
		select n.nspname || '.' || p.proname from pg_proc p join pg_namespace n on n.oid = p.pronamespace
		 where n.nspname not in ('pg_catalog', 'information_schema')
		   and has_function_privilege('tramo_inbox', p.oid, 'execute')
		   and not has_function_privilege('public', p.oid, 'execute')`)
	if err != nil {
		t.Fatal(err)
	}
	var fns []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		fns = append(fns, n)
	}
	if len(fns) != 1 || fns[0] != "inbox.address_tenant" {
		t.Fatalf("tramo_inbox may execute %v, want only inbox.address_tenant", fns)
	}
	for _, table := range []string{"tenants", "users", "memberships", "inbox.addresses", "inbox.senders", "inbox.receipts", "audit_log", "closes.closes"} {
		err := pgx.BeginFunc(ctx, f.db.Pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "set local role tramo_inbox"); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "select 1 from "+table+" limit 1")
			return err
		})
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "42501" {
			t.Errorf("tramo_inbox reading %s: %v, want permission denied", table, err)
		}
	}
	code := strings.TrimSuffix(f.to, "@"+domain)
	if got, ok, err := f.db.InboxTenant(ctx, strings.ToUpper(code)); err != nil || !ok || got != f.tenantA {
		t.Fatalf("lookup: %s %v %v", got, ok, err)
	}
	if _, ok, err := f.db.InboxTenant(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil || ok {
		t.Fatalf("unknown code found: %v %v", ok, err)
	}
}

func TestAnAuthorizedAuthenticatedMessageIsDelivered(t *testing.T) {
	f := setup(t, inbox.Limits{})
	var o opened
	r, err := f.svc.Receive(context.Background(), msg(f.to, "Tesorería <banco@example.com>",
		o.att("noviembre.csv", csv), o.att("resumen.PDF", pdf), o.att("virus.exe", []byte("MZ")), o.att("falso.pdf", []byte("MZ\x90\x00"))))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != inbox.Accepted || r.Sender != "banco@example.com" || len(r.Attachments) != 4 {
		t.Fatalf("receipt: %+v", r)
	}
	want := []struct{ status, reason string }{{inbox.Delivered, ""}, {inbox.Delivered, ""},
		{inbox.AttRefused, inbox.ReasonUnsupportedKind}, {inbox.AttRefused, inbox.ReasonUnsupportedKind}}
	for i, w := range want {
		a := r.Attachments[i]
		if a.Status != w.status || a.Reason != w.reason || a.SHA256 == "" {
			t.Errorf("attachment %d %s: %+v, want %s %s", i, a.Filename, a, w.status, w.reason)
		}
	}
	if r.Attachments[0].Ingest == nil || r.Attachments[0].Ingest.New != 3 {
		t.Fatalf("ingest outcome not kept: %+v", r.Attachments[0])
	}
	if f.ingest.n() != 2 || f.ingest.calls[0].Sender != "banco@example.com" || !bytes.Equal(f.ingest.calls[0].Data, csv) {
		t.Fatalf("ingest calls: %d", f.ingest.n())
	}
	list, err := f.svc.Receipts(as(f.tenantA, f.accountant))
	if err != nil || len(list) != 1 || list[0].ID != r.ID {
		t.Fatalf("accountant lists: %v %v", len(list), err)
	}
	if _, err := f.svc.Receipts(as(f.tenantA, f.viewer)); !errors.Is(err, inbox.ErrForbidden) {
		t.Fatalf("viewer lists receipts: %v", err)
	}
	if list, _ := f.svc.Receipts(as(f.tenantB, f.ownerB)); len(list) != 0 {
		t.Fatalf("tenant B sees A's receipts: %d", len(list))
	}
}

// Refusals that need nothing of the attachments read nothing of them.
func TestRefusalsReadNoAttachment(t *testing.T) {
	f := setup(t, inbox.Limits{AttachmentsPerMessage: 2, BytesPerAttachment: 1 << 20, BytesPerMessage: 2 << 20,
		MessagesPerDay: 100, MessagesPerSenderPerDay: 100, RefusedPerDay: 100})
	var o opened
	unauth := msg(f.to, "otro@example.com", o.att("a.csv", csv))
	noauth := msg(f.to, "banco@example.com", o.att("a.csv", csv))
	noauth.SPFAligned = false
	many := msg(f.to, "banco@example.com", o.att("a.csv", csv), o.att("b.csv", csv), o.att("c.csv", csv))
	for _, c := range []struct {
		m      inbox.Message
		reason string
	}{{unauth, inbox.ReasonSenderNotAuthorized}, {noauth, inbox.ReasonNotAuthenticated}, {many, inbox.ReasonTooManyAttachments}} {
		r, err := f.svc.Receive(context.Background(), c.m)
		if err != nil || r.Status != inbox.Refused || r.Reason != c.reason {
			t.Errorf("%s: %+v %v", c.reason, r, err)
		}
	}
	if o.bytes.Load() != 0 || f.ingest.n() != 0 {
		t.Fatalf("refused messages read %d bytes, ingested %d", o.bytes.Load(), f.ingest.n())
	}
	dkim := msg(f.to, "banco@example.com", o.att("a.csv", csv))
	dkim.SPFAligned, dkim.DKIMAligned = false, true
	if r, err := f.svc.Receive(context.Background(), dkim); err != nil || r.Status != inbox.Accepted {
		t.Fatalf("DKIM aligned alone: %+v %v", r, err)
	}
}

// A large attachment is cut at its limit, never read whole, and the rest
// of the message goes on; past the message's total it is refused whole.
func TestSizeLimitsCutTheReading(t *testing.T) {
	lim := inbox.Limits{AttachmentsPerMessage: 10, BytesPerAttachment: 1 << 20, BytesPerMessage: 3 << 20,
		MessagesPerDay: 100, MessagesPerSenderPerDay: 100, RefusedPerDay: 100}
	f := setup(t, lim)
	var o opened
	r, err := f.svc.Receive(context.Background(), msg(f.to, "banco@example.com", o.endless("grande.csv", 500<<20), o.att("ok.csv", csv)))
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != inbox.Accepted || r.Attachments[0].Reason != inbox.ReasonTooLarge || r.Attachments[0].Size != lim.BytesPerAttachment ||
		r.Attachments[1].Status != inbox.Delivered {
		t.Fatalf("receipt: %+v", r)
	}
	if read := o.bytes.Load(); read > lim.BytesPerAttachment+1+int64(len(csv)) {
		t.Fatalf("read %d bytes of a 500 MiB attachment", read)
	}

	var o2 opened
	big := func(n string) inbox.Attachment { return o2.endless(n, 900<<10) }
	r, err = f.svc.Receive(context.Background(), msg(f.to, "banco@example.com", big("1.csv"), big("2.csv"), big("3.csv"), big("4.csv"), big("5.csv")))
	if err != nil || r.Status != inbox.Refused || r.Reason != inbox.ReasonMessageTooLarge {
		t.Fatalf("message over its total: %+v %v", r, err)
	}
	if read := o2.bytes.Load(); read > lim.BytesPerMessage+1 {
		t.Fatalf("read %d bytes, past the message's total", read)
	}
	if f.ingest.n() != 1 {
		t.Fatalf("ingested %d, want only ok.csv", f.ingest.n())
	}
}

// A retry of the same message returns its receipt and ingests nothing again.
func TestARetryIsTheSameReceipt(t *testing.T) {
	f := setup(t, inbox.Limits{})
	var o opened
	m := msg(f.to, "banco@example.com", o.att("a.csv", csv))
	first, err := f.svc.Receive(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.Receive(context.Background(), m)
	if err != nil || again.ID != first.ID || f.ingest.n() != 1 {
		t.Fatalf("retry: %v %v, ingested %d", again.ID, err, f.ingest.n())
	}
}

// Without ingestion yet, nothing is stored and the receipt says so.
func TestWithoutIngestionAttachmentsAreNotDelivered(t *testing.T) {
	f := setup(t, inbox.Limits{})
	f.svc.Ingest = nil
	var o opened
	r, err := f.svc.Receive(context.Background(), msg(f.to, "banco@example.com", o.att("a.csv", csv)))
	if err != nil || r.Status != inbox.Accepted || r.Attachments[0].Status != inbox.NotDelivered {
		t.Fatalf("%+v %v", r, err)
	}
}

// Wrong domain, unknown code, a rotated address: nothing stored. A removed
// sender: refused.
func TestAddressesAndSendersChange(t *testing.T) {
	f := setup(t, inbox.Limits{})
	var o opened
	ctx := context.Background()
	code := strings.TrimSuffix(f.to, "@"+domain)
	for _, to := range []string{code + "@otro.test", "aaaaaaaaaaaaaaaaaaaaaaaaaa@" + domain, "no es una direccion"} {
		if _, err := f.svc.Receive(ctx, msg(to, "banco@example.com", o.att("a.csv", csv))); !errors.Is(err, inbox.ErrUnknownAddress) {
			t.Errorf("to %q: %v", to, err)
		}
	}
	mb, err := f.svc.Rotate(as(f.tenantA, f.owner))
	if err != nil || mb.Code == code {
		t.Fatalf("rotate: %v", err)
	}
	if _, err := f.svc.Receive(ctx, msg(f.to, "banco@example.com", o.att("a.csv", csv))); !errors.Is(err, inbox.ErrUnknownAddress) {
		t.Fatalf("old address after rotation: %v", err)
	}
	newTo := f.svc.Address(mb.Code)
	if r, err := f.svc.Receive(ctx, msg(newTo, "banco@example.com", o.att("a.csv", csv))); err != nil || r.Status != inbox.Accepted {
		t.Fatalf("new address: %+v %v", r, err)
	}
	if n := countRows(t, f.env, `select count(*) from inbox.receipts`); n != 1 {
		t.Fatalf("%d receipts, want only the one to the new address", n)
	}
	got, _ := f.svc.Get(as(f.tenantA, f.owner))
	if err := f.svc.RemoveSender(as(f.tenantA, f.owner), got.Senders[0].ID); err != nil {
		t.Fatal(err)
	}
	if r, err := f.svc.Receive(ctx, msg(newTo, "banco@example.com", o.att("a.csv", csv))); err != nil || r.Reason != inbox.ReasonSenderNotAuthorized {
		t.Fatalf("removed sender: %+v %v", r, err)
	}
}

// The day's limits hold under concurrent deliveries; refused receipts stop
// at their own cap and the rest is dropped.
func TestDailyLimitsUnderConcurrency(t *testing.T) {
	f := setup(t, inbox.Limits{AttachmentsPerMessage: 10, BytesPerAttachment: 1 << 20, BytesPerMessage: 2 << 20,
		MessagesPerDay: 3, MessagesPerSenderPerDay: 3, RefusedPerDay: 2})
	var o opened
	var mu sync.Mutex
	count := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.svc.Receive(context.Background(), msg(f.to, "banco@example.com", o.att("a.csv", csv)))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, inbox.ErrDropped):
				count["dropped"]++
			case err != nil:
				count["error: "+err.Error()]++
			default:
				count[r.Status+"/"+r.Reason]++
			}
		}()
	}
	wg.Wait()
	if count["accepted/"] != 3 || count["refused/"+inbox.ReasonDailyLimit] != 2 || count["dropped"] != 3 {
		t.Fatalf("outcomes %v, want 3 accepted, 2 refused for the daily limit, 3 dropped", count)
	}
	if f.ingest.n() != 3 {
		t.Fatalf("ingested %d, want 3", f.ingest.n())
	}
}

func TestSenderDailyLimit(t *testing.T) {
	f := setup(t, inbox.Limits{AttachmentsPerMessage: 10, BytesPerAttachment: 1 << 20, BytesPerMessage: 2 << 20,
		MessagesPerDay: 10, MessagesPerSenderPerDay: 1, RefusedPerDay: 10})
	if _, err := f.svc.AddSender(as(f.tenantA, f.owner), "otro@example.com"); err != nil {
		t.Fatal(err)
	}
	var o opened
	ctx := context.Background()
	for i, c := range []struct{ from, status, reason string }{
		{"banco@example.com", inbox.Accepted, ""},
		{"banco@example.com", inbox.Refused, inbox.ReasonSenderDailyLimit},
		{"otro@example.com", inbox.Accepted, ""},
	} {
		r, err := f.svc.Receive(ctx, msg(f.to, c.from, o.att("a.csv", csv)))
		if err != nil || r.Status != c.status || r.Reason != c.reason {
			t.Errorf("%d: %+v %v", i, r, err)
		}
	}
}

// Only the owner changes the mailbox; owner, accountant and operator see it.
func TestOnlyTheOwnerChangesTheMailbox(t *testing.T) {
	f := setup(t, inbox.Limits{})
	for _, u := range []uuid.UUID{f.accountant, f.viewer} {
		ctx := as(f.tenantA, u)
		if _, err := f.svc.Rotate(ctx); !errors.Is(err, inbox.ErrForbidden) {
			t.Errorf("rotate by %s: %v", u, err)
		}
		if _, err := f.svc.AddSender(ctx, "x@example.com"); !errors.Is(err, inbox.ErrForbidden) {
			t.Errorf("add sender by %s: %v", u, err)
		}
	}
	if _, err := f.svc.Get(as(f.tenantA, f.viewer)); !errors.Is(err, inbox.ErrForbidden) {
		t.Fatalf("viewer sees the mailbox: %v", err)
	}
	mb, err := f.svc.Get(as(f.tenantA, f.accountant))
	if err != nil || mb.Code == "" || len(mb.Senders) != 1 || mb.Senders[0].Email != "banco@example.com" {
		t.Fatalf("accountant sees: %+v %v", mb, err)
	}
	if _, err := f.svc.AddSender(as(f.tenantA, f.owner), "BANCO@example.com"); !errors.Is(err, inbox.ErrAlreadyAuthorized) {
		t.Fatalf("same sender twice: %v", err)
	}
	for _, bad := range []string{"Banco <b@example.com>", "a@example.com, b@example.com", "no-es-mail"} {
		if _, err := f.svc.AddSender(as(f.tenantA, f.owner), bad); !errors.Is(err, inbox.ErrInvalidEmail) {
			t.Errorf("sender %q: %v", bad, err)
		}
	}
	if other, _ := f.svc.Get(as(f.tenantB, f.ownerB)); other.Code != "" || len(other.Senders) != 0 {
		t.Fatalf("tenant B sees A's mailbox: %+v", other)
	}
	if err := f.svc.RemoveSender(as(f.tenantB, f.ownerB), mb.Senders[0].ID); !errors.Is(err, inbox.ErrNotFound) {
		t.Fatalf("tenant B removes A's sender: %v", err)
	}
}
