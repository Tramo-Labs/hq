package documents

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func seedTenants(t *testing.T, d *db.DB) (a, b db.Scope) {
	t.Helper()
	ctx := context.Background()
	err := d.System(ctx, func(tx pgx.Tx) error {
		for i, s := range []*db.Scope{&a, &b} {
			s.TenantID = uuid.New()
			slug := []string{"alfa", "beta"}[i]
			if _, err := tx.Exec(ctx, `insert into tenants (id, slug, name) values ($1, $2, $2)`, s.TenantID, slug); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestPGSaveAndDuplicates(t *testing.T) {
	d := dbtest.New(t)
	a, b := seedTenants(t, d)
	ctxA := db.WithScope(context.Background(), a)
	ctxB := db.WithScope(context.Background(), b)
	s := &PGStore{DB: d}

	pdfQR := buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: pdfQR})
	id, err := s.Save(ctxA, pdfQR, inv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctxA, id)
	if err != nil {
		t.Fatal(err)
	}
	if f, _ := got.Get(AuthCode); f.Value != "76451234567890" || f.Source != SourceQR {
		t.Errorf("round trip %+v", f)
	}

	// The same file again.
	if _, err := s.Save(ctxA, pdfQR, inv); !errors.Is(err, ErrSameDocument) {
		t.Errorf("same file: %v", err)
	}
	// Another file (text only) that reads the same invoice: duplicate.
	pdfText := buildPDF(t, pdfSpec{Lines: invoiceLines})
	inv2 := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: pdfText})
	if _, err := s.Save(ctxA, pdfText, inv2); !errors.Is(err, ErrDuplicateInvoice) {
		t.Errorf("same invoice from another file: %v", err)
	}
	// An uncertain reading does not take part in the duplicate key: the same
	// numbers read without the tenant's CUIT (issuer ambiguous) are stored
	// and left for review instead of being called a duplicate.
	pdfOther := buildPDF(t, pdfSpec{Lines: append([]string{"COPIA"}, invoiceLines...)})
	inv3 := extract(t, &Extractor{}, Input{PDF: pdfOther})
	if _, err := s.Save(ctxA, pdfOther, inv3); err != nil {
		t.Errorf("uncertain reading: %v", err)
	}

	// The list reads every key column back from trusted fields, and each
	// filter narrows it.
	listed, err := s.List(ctxA, ListFilter{}, 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list: %d rows (%v)", len(listed), err)
	}
	var qr *Summary
	for i := range listed {
		if listed[i].ID == id {
			qr = &listed[i]
		}
	}
	if qr == nil {
		t.Fatal("the QR invoice is not listed")
	}
	if qr.IssuerCUIT == nil || *qr.IssuerCUIT != "30000000015" || qr.DocType == nil || *qr.DocType != 1 ||
		qr.PointOfSale == nil || *qr.PointOfSale != 2 || qr.Number == nil || *qr.Number != 1234 ||
		qr.Total == nil || *qr.Total != 12100050 || qr.IssueDate == nil || qr.IssueDate.Format("2006-01-02") != "2026-11-05" ||
		!qr.AutoEligible || qr.MinConfidence <= 0 || len(qr.Missing) != 0 || qr.ItemReason != "no_tenant_cuit" || qr.CreatedAt.IsZero() {
		t.Errorf("listed QR invoice %+v", *qr)
	}
	ar, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	yes, no := true, false
	for _, c := range []struct {
		name string
		f    ListFilter
		want int
	}{
		{"this month", ListFilter{Period: time.Now().In(ar).Format("2006-01")}, 2},
		{"another month", ListFilter{Period: "2000-01"}, 0},
		{"eligible", ListFilter{AutoEligible: &yes}, 1},
		{"not eligible", ListFilter{AutoEligible: &no}, 1},
		{"to review: no item without the tenant CUIT", ListFilter{Review: true}, 2},
	} {
		got, err := s.List(ctxA, c.f, 10)
		if err != nil || len(got) != c.want {
			t.Errorf("list %s: %d rows, want %d (%v)", c.name, len(got), c.want, err)
		}
	}
	if got, err := s.List(ctxA, ListFilter{}, 1); err != nil || len(got) != 1 {
		t.Errorf("limit 1: %d rows (%v)", len(got), err)
	}

	// Another tenant: sees nothing, and its own copy is not a duplicate.
	var n int
	if err := d.InTenant(ctxB, func(tx pgx.Tx) error {
		return tx.QueryRow(ctxB, `select count(*) from documents.invoices`).Scan(&n)
	}); err != nil || n != 0 {
		t.Errorf("tenant b sees %d invoices (%v)", n, err)
	}
	if _, err := s.Get(ctxB, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("tenant b reads a's invoice by id: %v", err)
	}
	if _, err := s.Save(ctxB, pdfQR, inv); err != nil {
		t.Errorf("tenant b own copy: %v", err)
	}
	// B cannot write an invoice for A, nor change A's invoice.
	err = d.InTenant(ctxB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctxB, `insert into documents.invoices (tenant_id, sha256, fields, trail, auto_eligible, min_confidence)
			values ($1, '\x00', '{}', '[]', false, 0)`, a.TenantID)
		return err
	})
	if err == nil {
		t.Error("tenant b wrote an invoice for tenant a")
	}
	var changed int64
	err = d.InTenant(ctxB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctxB, `update documents.invoices set auto_eligible = true where id = $1`, id)
		changed = tag.RowsAffected()
		return err
	})
	if err != nil || changed != 0 {
		t.Errorf("tenant b changed a's invoice: %d rows, %v", changed, err)
	}
}

// With the tenant's CUIT on file the eligible QR invoice gets its open item,
// so the review filter keeps only the ambiguous one.
func TestPGListReview(t *testing.T) {
	d := dbtest.New(t)
	a, _ := seedTenants(t, d)
	ctx := db.WithScope(context.Background(), a)
	if err := d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update tenants set cuit = $1 where id = $2`, int64(30000000023), a.TenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s := &PGStore{DB: d}
	pdfQR := buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})
	good, err := s.Save(ctx, pdfQR, extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: pdfQR}))
	if err != nil {
		t.Fatal(err)
	}
	pdfOther := buildPDF(t, pdfSpec{Lines: append([]string{"COPIA"}, invoiceLines...)})
	odd, err := s.Save(ctx, pdfOther, extract(t, &Extractor{}, Input{PDF: pdfOther}))
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx, ListFilter{}, 10)
	if err != nil || len(all) != 2 {
		t.Fatalf("list: %d rows (%v)", len(all), err)
	}
	review, err := s.List(ctx, ListFilter{Review: true}, 10)
	if err != nil || len(review) != 1 || review[0].ID != odd {
		t.Errorf("review: %+v (%v), want only %s", review, err, odd)
	}
	yes, no := true, false
	if got, err := s.List(ctx, ListFilter{AutoEligible: &yes}, 10); err != nil || len(got) != 1 || got[0].ID != good {
		t.Errorf("eligible: %+v (%v), want only %s", got, err, good)
	}
	if got, err := s.List(ctx, ListFilter{AutoEligible: &no}, 10); err != nil || len(got) != 1 || got[0].ID != odd {
		t.Errorf("not eligible: %+v (%v), want only %s", got, err, odd)
	}
	for _, sm := range all {
		if sm.ID == good && (sm.ItemReason != "" || !sm.AutoEligible) {
			t.Errorf("good invoice %+v", sm)
		}
	}
}
