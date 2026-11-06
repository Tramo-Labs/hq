package app_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/inbox"
)

func TestInboxOverHTTP(t *testing.T) {
	deps := newDeps(t)
	deps.InboxDomain = "casilla.test"
	c := client{t, serve(t, deps)}
	who := c.roles(deps, "molino-ib")
	own := who["owner"].access

	r := c.do("GET", "/v1/inbox", own, nil)
	if r.status != 200 || r.body["address"] != nil {
		t.Fatalf("before an address: %d %s", r.status, r.raw)
	}
	if lim, _ := r.body["limits"].(map[string]any); lim["bytesPerAttachment"] != float64(20<<20) {
		t.Fatalf("limits: %v", r.body["limits"])
	}
	for _, role := range []string{"approver", "viewer"} {
		if r := c.do("GET", "/v1/inbox", who[role].access, nil); r.status != 403 {
			t.Errorf("%s sees the mailbox: %d", role, r.status)
		}
	}
	for _, role := range []string{"approver", "viewer", "accountant", "operator"} {
		if r := c.do("POST", "/v1/inbox/address", who[role].access, nil, idem()...); r.status != 403 {
			t.Errorf("%s rotates: %d", role, r.status)
		}
		if r := c.do("POST", "/v1/inbox/senders", who[role].access, map[string]any{"email": "a@b.test"}, idem()...); r.status != 403 {
			t.Errorf("%s adds a sender: %d", role, r.status)
		}
	}
	if r := c.do("POST", "/v1/inbox/address", own, nil); r.status != 400 {
		t.Fatalf("rotate without Idempotency-Key: %d", r.status)
	}
	r = c.do("POST", "/v1/inbox/address", own, nil, idem()...)
	addr := r.str("address")
	if r.status != 201 || !strings.HasSuffix(addr, "@casilla.test") || len(addr) != 26+len("@casilla.test") {
		t.Fatalf("rotate: %d %s", r.status, r.raw)
	}
	s := c.do("POST", "/v1/inbox/senders", own, map[string]any{"email": "Banco@Example.com"}, idem()...)
	if s.status != 201 || s.str("email") != "banco@example.com" {
		t.Fatalf("add sender: %d %s", s.status, s.raw)
	}
	if r := c.do("POST", "/v1/inbox/senders", own, map[string]any{"email": "banco@example.com"}, idem()...); r.status != 409 || r.str("code") != "already_authorized" {
		t.Fatalf("same sender: %d %s", r.status, r.raw)
	}
	if r := c.do("GET", "/v1/inbox", who["accountant"].access, nil); r.str("address") != addr || len(r.body["senders"].([]any)) != 1 {
		t.Fatalf("accountant sees: %s", r.raw)
	}

	svc := &inbox.Service{DB: deps.DB, Domain: "casilla.test"}
	rc, err := svc.Receive(context.Background(), inbox.Message{To: addr, From: "banco@example.com", MessageID: "<1@mx>", SPFAligned: true,
		Attachments: []inbox.Attachment{{Filename: "nov.csv", Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader([]byte("fecha;importe\n"))), nil
		}}}})
	if err != nil || rc.Status != inbox.Accepted {
		t.Fatalf("receive: %+v %v", rc, err)
	}
	list := c.do("GET", "/v1/inbox/receipts", who["operator"].access, nil)
	items, _ := list.body["items"].([]any)
	if list.status != 200 || len(items) != 1 {
		t.Fatalf("receipts: %d %s", list.status, list.raw)
	}
	item := items[0].(map[string]any)
	att := item["attachments"].([]any)[0].(map[string]any)
	if item["sender"] != "banco@example.com" || item["status"] != "accepted" || att["status"] != "not_delivered" || att["filename"] != "nov.csv" {
		t.Fatalf("receipt: %v", item)
	}
	for _, k := range []string{"subject", "body", "headers"} {
		if _, ok := item[k]; ok {
			t.Fatalf("receipt carries %s", k)
		}
	}
	if r := c.do("GET", "/v1/inbox/receipts", who["viewer"].access, nil); r.status != 403 {
		t.Fatalf("viewer lists receipts: %d", r.status)
	}
	if r := c.do("DELETE", "/v1/inbox/senders/"+s.str("id"), own, nil); r.status != 204 {
		t.Fatalf("remove sender: %d %s", r.status, r.raw)
	}
	if r := c.do("DELETE", "/v1/inbox/senders/"+s.str("id"), own, nil); r.status != 404 {
		t.Fatalf("remove twice: %d", r.status)
	}
}

// With no domain configured the address exists but is not shown.
func TestInboxAddressNeedsADomain(t *testing.T) {
	deps := newDeps(t)
	c := client{t, serve(t, deps)}
	own := c.registerAndLogin("molino-ib2", "own@ib2.test")
	if r := c.do("POST", "/v1/inbox/address", own.access, nil, idem()...); r.status != 201 || r.body["address"] != nil {
		t.Fatalf("rotate without a domain: %d %s", r.status, r.raw)
	}
}
