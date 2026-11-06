package documents

import (
	"strings"
	"testing"
)

// goodPayload: issuer 30000000015, receiver 30000000023, factura A 0002-1234,
// $121.000,50 in pesos (see documents_test.go).

func draftFrom(t *testing.T, in Input, own string) (ItemDraft, string) {
	t.Helper()
	inv := extract(t, &Extractor{OwnCUIT: own}, in)
	return DraftItem(inv, own)
}

// The tenant received it (QR receiver is the tenant): a payable with the
// QR's confidence, eligible for automatic.
func TestItemPayableFromQR(t *testing.T) {
	d, reason := draftFrom(t, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})}, receiver)
	if reason != "" {
		t.Fatalf("no item: %s", reason)
	}
	if d.Kind != "payable" || d.Amount != -12100050 || d.Party != issuer || !d.DirectionCertain {
		t.Errorf("draft %+v", d)
	}
	if d.Confidence < AutoThreshold {
		t.Errorf("confidence %.2f; a QR invoice with every field trusted should reach automatic", d.Confidence)
	}
	if strings.Join(d.Refs, ",") != "0002-00001234,00001234" {
		t.Errorf("refs %v", d.Refs)
	}
}

// The tenant issued it (QR issuer is the tenant): a receivable.
func TestItemReceivableFromQR(t *testing.T) {
	d, reason := draftFrom(t, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})}, issuer)
	if reason != "" || d.Kind != "receivable" || d.Amount != 12100050 || d.Party != receiver || !d.DirectionCertain {
		t.Errorf("draft %+v reason %q", d, reason)
	}
}

// The trap: an invoice the tenant issued, read without QR. The text rules
// leave the tenant's own CUIT out of the issuer candidates, so the other
// CUIT looks like the issuer and the invoice looks payable. It must never
// be a payable at automatic confidence.
func TestItemTenantIssuedWithoutQRIsNeverAutomatic(t *testing.T) {
	d, reason := draftFrom(t, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines})}, issuer)
	if reason != "" {
		t.Fatalf("no item: %s", reason)
	}
	if d.DirectionCertain {
		t.Error("direction read from text counted as certain")
	}
	if d.Kind == "payable" && d.Confidence >= AutoThreshold {
		t.Errorf("tenant's own invoice became a payable at %.2f", d.Confidence)
	}
	if d.Confidence > DirectionCap {
		t.Errorf("confidence %.2f over the cap for an uncertain direction", d.Confidence)
	}
}

func TestItemReasons(t *testing.T) {
	foreign := strings.Replace(goodPayload, `"moneda":"PES","ctz":1`, `"moneda":"DOL","ctz":1050.5`, 1)
	noTotal := []string{"FACTURA A", "COD. 01", "CUIT: 30-00000001-5", "Fecha de Emisión: 05/11/2026", "CAE N°: 76451234567890"}
	cases := map[string]struct {
		in   Input
		own  string
		want string
	}{
		"no tenant cuit":   {Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})}, "", NoItemTenantCUIT},
		"foreign currency": {Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(foreign)})}, receiver, NoItemCurrency},
		"no total":         {Input{PDF: buildPDF(t, pdfSpec{Lines: noTotal})}, receiver, NoItemTotalOrDate},
	}
	for name, c := range cases {
		if _, reason := draftFrom(t, c.in, c.own); reason != c.want {
			t.Errorf("%s: reason %q; want %q", name, reason, c.want)
		}
	}
	// No issuer at all: the side is unknown.
	inv := newInvoice()
	inv.Fields[Total] = Field{Value: "100.00", Source: SourceQR, Confidence: qrConfidence}
	inv.Fields[IssueDate] = Field{Value: "2026-10-01", Source: SourceQR, Confidence: qrConfidence}
	if _, reason := DraftItem(inv, receiver); reason != NoItemDirection {
		t.Errorf("no issuer: %q", reason)
	}
	// A total read with low confidence is not trusted.
	inv.Fields[Total] = Field{Value: "100.00", Source: SourcePDFText, Confidence: confLabeledMany}
	inv.Fields[IssuerCUIT] = Field{Value: issuer, Source: SourceQR, Confidence: qrConfidence}
	if _, reason := DraftItem(inv, receiver); reason != NoItemTotalOrDate {
		t.Errorf("untrusted total: %q", reason)
	}
}

// A credit note reverses the sign: received, it is money in.
func TestItemCreditNote(t *testing.T) {
	nc := strings.Replace(goodPayload, `"tipoCmp":1`, `"tipoCmp":3`, 1)
	d, reason := draftFrom(t, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(nc)})}, receiver)
	if reason != "" || d.Amount != 12100050 {
		t.Errorf("credit note received: %+v %q", d, reason)
	}
}
