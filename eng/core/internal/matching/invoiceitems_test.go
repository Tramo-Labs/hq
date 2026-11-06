package matching

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/documents"
)

// Synthetic CUITs: the tenant and a supplier (valid check digits, all zero body).
const (
	tenantCUIT   = "30000000023"
	supplierCUIT = "30000000015"
)

func (e pgEnv) setTenantCUIT(t *testing.T, cuit string) {
	t.Helper()
	ctx := context.Background()
	if err := e.d.System(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `update tenants set cuit = $1 where id = $2`, cuit, e.a.TenantID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// invoice builds an extracted invoice: every field from the given source.
func invoice(src documents.Source, conf float64, total, date, number string, withReceiver bool) *documents.Invoice {
	f := func(v string) documents.Field { return documents.Field{Value: v, Source: src, Confidence: conf} }
	inv := &documents.Invoice{Fields: map[documents.Name]documents.Field{
		documents.IssuerCUIT: f(supplierCUIT), documents.DocType: f("1"), documents.PointOfSale: f("2"),
		documents.Number: f(number), documents.IssueDate: f(date), documents.Total: f(total),
		documents.AuthCode: f("76451234567890"),
	}}
	if withReceiver {
		inv.Fields[documents.ReceiverDoc] = f(tenantCUIT)
		inv.Fields[documents.Currency] = f("PES")
	}
	return inv
}

func (e pgEnv) docs() *documents.PGStore {
	return &documents.PGStore{DB: e.d, OnSaved: e.store.InvoiceItemTx}
}

func (e pgEnv) descriptionOf(t *testing.T, movement string) string {
	t.Helper()
	var d string
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(e.ctx, `select description from ledger.movements where id = $1`, movement).Scan(&d)
	}); err != nil {
		t.Fatal(err)
	}
	return d
}

// F2: uploaded invoices generate the items the month runs against. The QR
// invoice of the supplier payment is matched automatically; the text-only
// invoice of the bank commission is only suggested.
func TestPGInvoicesGenerateItems(t *testing.T) {
	e := newPGEnv(t)
	e.setTenantCUIT(t, tenantCUIT)
	ds := e.docs()

	// "PAGO PROVEEDOR EJEMPLO SA" is -120.500,50 on Oct 2 in the Bancor file.
	qr, err := ds.Save(e.ctx, []byte("qr invoice"), invoice(documents.SourceQR, 0.98, "120500.50", "2026-10-01", "7001", true))
	if err != nil {
		t.Fatal(err)
	}
	// "COMISION MANTENIMIENTO CUENTA" is -8.500,00 on Oct 2.
	text, err := ds.Save(e.ctx, []byte("text invoice"), invoice(documents.SourcePDFText, 0.9, "8500.00", "2026-10-02", "7002", false))
	if err != nil {
		t.Fatal(err)
	}
	var items int
	var qrConf, textConf float64
	var qrAmount int64
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(e.ctx, `select count(*) from matching.items where source = 'document'`).Scan(&items); err != nil {
			return err
		}
		if err := tx.QueryRow(e.ctx, `select confidence, amount from matching.items where invoice_id = $1`, qr).Scan(&qrConf, &qrAmount); err != nil {
			return err
		}
		return tx.QueryRow(e.ctx, `select confidence from matching.items where invoice_id = $1`, text).Scan(&textConf)
	}); err != nil {
		t.Fatal(err)
	}
	if items != 2 || qrAmount != -12050050 || qrConf < DefaultConfig.AutoConfidence || textConf >= DefaultConfig.AutoConfidence {
		t.Fatalf("items %d, qr %d at %.2f, text at %.2f", items, qrAmount, qrConf, textConf)
	}

	r, _ := e.run(t)
	var autoPay, suggestedFee bool
	for _, m := range r.Matches {
		desc := e.descriptionOf(t, m.MovementIDs[0])
		if desc == "PAGO PROVEEDOR EJEMPLO SA" && m.Tray == Auto {
			autoPay = true
		}
		if desc == "COMISION MANTENIMIENTO CUENTA" && m.Tray == Suggested && m.LowConfidence {
			suggestedFee = true
		}
	}
	if !autoPay || !suggestedFee {
		t.Errorf("auto supplier payment %v, suggested commission %v: %+v", autoPay, suggestedFee, r.Matches)
	}
	if v := value(t, byKey(t, mustMetrics(t, e, "2026-10"))["automation_rate"]); v != 30 {
		t.Errorf("automation_rate %v; want 30 (cheque, transfer and the supplier invoice, of 10)", v)
	}
}

// An invoice that generates no item says why, and shows to review.
func TestPGInvoiceWithoutItemSaysWhy(t *testing.T) {
	e := newPGEnv(t)
	ds := e.docs()
	// No CUIT on file for the tenant.
	id, err := ds.Save(e.ctx, []byte("no cuit"), invoice(documents.SourceQR, 0.98, "100.00", "2026-10-01", "8001", true))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ds.Get(e.ctx, id)
	if err != nil || got.ItemReason != documents.NoItemTenantCUIT {
		t.Fatalf("reason %q (%v)", got.ItemReason, err)
	}
	review, err := ds.List(e.ctx, documents.ListFilter{Review: true}, 10)
	if err != nil || len(review) != 1 || review[0].ItemReason != documents.NoItemTenantCUIT {
		t.Errorf("review list %+v %v", review, err)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.items where source = 'document'`); n != 0 {
		t.Errorf("%d items for an invoice that cannot have one", n)
	}
}

// F21, the client's case: a QR invoice issued by a supplier to another
// company (beta), uploaded by alfa. Neither trusted issuer nor trusted
// receiver is alfa: not this tenant's, no item, never auto eligible, listed
// to review with its reason.
func TestPGInvoiceOfAnotherCompany(t *testing.T) {
	e := newPGEnv(t)
	e.setTenantCUIT(t, "30000000031") // alfa; the invoice is between the supplier and 30000000023
	ds := e.docs()
	inv := invoice(documents.SourceQR, 0.98, "120500.50", "2026-10-01", "9001", true)
	id, err := ds.Save(e.ctx, []byte("someone else's invoice"), inv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ds.Get(e.ctx, id)
	if err != nil || got.ItemReason != documents.NoItemNotThisTenant || got.AutoEligible() {
		t.Fatalf("reason %q, auto eligible %v (%v)", got.ItemReason, got.AutoEligible(), err)
	}
	review, err := ds.List(e.ctx, documents.ListFilter{Review: true}, 10)
	if err != nil || len(review) != 1 || review[0].AutoEligible || review[0].ItemReason != documents.NoItemNotThisTenant {
		t.Errorf("review list %+v %v", review, err)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.items where source = 'document'`); n != 0 {
		t.Errorf("%d items for another company's invoice", n)
	}
	// The same invoice for the right tenant is a normal payable.
	e.setTenantCUIT(t, tenantCUIT)
	if d, reason := documents.DraftItem(invoice(documents.SourceQR, 0.98, "120500.50", "2026-10-01", "9001", true), tenantCUIT); reason != "" || d.Kind != "payable" {
		t.Errorf("for its receiver: %+v %q", d, reason)
	}
}

// The item follows its invoice, and an item a match in effect settles does
// not change or disappear in silence.
func TestPGInvoiceItemFollowsAndIsGuarded(t *testing.T) {
	e := newPGEnv(t)
	e.setTenantCUIT(t, tenantCUIT)
	ds := e.docs()
	settled, err := ds.Save(e.ctx, []byte("qr invoice"), invoice(documents.SourceQR, 0.98, "120500.50", "2026-10-01", "7001", true))
	if err != nil {
		t.Fatal(err)
	}
	loose, err := ds.Save(e.ctx, []byte("loose invoice"), invoice(documents.SourceQR, 0.98, "777.00", "2026-10-01", "7003", true))
	if err != nil {
		t.Fatal(err)
	}
	e.run(t) // the first one is now settled by an automatic match

	exec := func(q string, args ...any) error {
		return e.d.InTenant(e.ctx, func(tx pgx.Tx) error { _, err := tx.Exec(e.ctx, q, args...); return err })
	}
	if err := exec(`delete from documents.invoices where id = $1`, settled); err == nil || !strings.Contains(err.Error(), "settled by match") {
		t.Errorf("deleting the invoice of a settled item: %v", err)
	}
	if err := exec(`update matching.items set amount = amount + 1 where invoice_id = $1`, settled); err == nil {
		t.Error("a settled item changed in silence")
	}
	// The same item reloaded unchanged is fine (nothing changes).
	if err := e.d.InTenant(e.ctx, func(tx pgx.Tx) error {
		return e.store.InvoiceItemTx(e.ctx, tx, settled, documents.ItemDraft{Kind: "payable",
			Date: day(1), Amount: -12050050, Refs: []string{"0002-00007001", "00007001"}, Party: supplierCUIT, Confidence: 0.98})
	}); err != nil {
		t.Errorf("unchanged reload of a settled item: %v", err)
	}
	// The loose invoice goes, and its item with it.
	if err := exec(`delete from documents.invoices where id = $1`, loose); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, e.ctx, `select count(*) from matching.items where invoice_id = $1`, loose); n != 0 {
		t.Errorf("item of a deleted invoice still there")
	}
}
