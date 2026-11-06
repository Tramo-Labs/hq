package documents

import (
	"context"
	"errors"
	"os"
	"testing"

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
