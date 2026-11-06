package documents

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"
)

// Synthetic CUITs with valid check digits; none belongs to a real taxpayer
// as far as we know (the body is all zeros).
const (
	issuer   = "30000000015"
	receiver = "30000000023" // the tenant
	other    = "30000000031"
)

const goodPayload = `{"ver":1,"fecha":"2026-11-05","cuit":30000000015,"ptoVta":2,"tipoCmp":1,"nroCmp":1234,` +
	`"importe":121000.5,"moneda":"PES","ctz":1,"tipoDocRec":80,"nroDocRec":30000000023,"tipoCodAut":"E","codAut":76451234567890}`

var invoiceLines = []string{
	"FACTURA A",
	"COD. 01",
	"EMPRESA EJEMPLO EMISORA SA",
	"CUIT: 30-00000001-5",
	"Punto de Venta: 00002   Comp. Nro: 00001234",
	"Fecha de Emisión: 05/11/2026",
	"Cliente: EMPRESA EJEMPLO RECEPTORA SRL   CUIT: 30-00000002-3",
	"Subtotal: $ 100.000,41",
	"IVA 21%: $ 21.000,09",
	"Importe Total: $ 121.000,50",
	"CAE N°: 76451234567890",
	"Fecha de Vto. de CAE: 15/11/2026",
}

func TestParseQRURLOfficialExample(t *testing.T) {
	// The example printed in ARCA's QR specification (eng/arca/manuales/qr.txt).
	u := "https://www.afip.gob.ar/fe/qr/?p=eyJ2ZXIiOjEsImZlY2hhIjoiMjAyMC0xMC0xMyIsImN1" +
		"aXQiOjMwMDAwMDAwMDA3LCJwdG9WdGEiOjEwLCJ0aXBvQ21wIjoxLCJucm9DbXAiO" +
		"jk0LCJpbXBvcnRlIjoxMjEwMCwibW9uZWRhIjoiRE9MIiwiY3R6Ijo2NSwidGlwb0RvY1JlYyI" +
		"6ODAsIm5yb0RvY1JlYyI6MjAwMDAwMDAwMDEsInRpcG9Db2RBdXQiOiJFIiwiY29kQXV" +
		"0Ijo3MDQxNzA1NDM2NzQ3Nn0="
	q, err := ParseQRURL(u)
	if err != nil {
		t.Fatal(err)
	}
	f := q.Fields()
	want := map[Name]string{IssuerCUIT: "30000000007", PointOfSale: "10", DocType: "1", Number: "94",
		IssueDate: "2020-10-13", Total: "12100.00", Currency: "DOL", Rate: "65", ReceiverType: "80",
		ReceiverDoc: "20000000001", AuthType: "E", AuthCode: "70417054367476"}
	for n, v := range want {
		if f[n].Value != v || f[n].Source != SourceQR {
			t.Errorf("%s = %q (%s); want %q", n, f[n].Value, f[n].Source, v)
		}
	}
}

func TestParseQRURLRejects(t *testing.T) {
	bad := map[string]string{
		"other host":      "https://www.example.com/fe/qr/?p=" + strings.TrimPrefix(qrURL(goodPayload), "https://www.arca.gob.ar/fe/qr/?p="),
		"no payload":      "https://www.arca.gob.ar/fe/qr/",
		"not base64":      "https://www.arca.gob.ar/fe/qr/?p=%%%%",
		"bad check digit": qrURL(strings.Replace(goodPayload, "30000000015", "30000000016", 1)),
		"short codAut":    qrURL(strings.Replace(goodPayload, "76451234567890", "7645123456789", 1)),
		"three decimals":  qrURL(strings.Replace(goodPayload, "121000.5", "121000.505", 1)),
		"exponent":        qrURL(strings.Replace(goodPayload, "121000.5", "1.21e5", 1)),
		"negative total":  qrURL(strings.Replace(goodPayload, "121000.5", "-1", 1)),
		"version 2":       qrURL(strings.Replace(goodPayload, `"ver":1`, `"ver":2`, 1)),
		"unknown field":   qrURL(strings.Replace(goodPayload, `"ver":1`, `"ver":1,"x":1`, 1)),
		"auth type":       qrURL(strings.Replace(goodPayload, `"E"`, `"X"`, 1)),
		"bad date":        qrURL(strings.Replace(goodPayload, "2026-11-05", "05/11/2026", 1)),
		"point of sale 0": qrURL(strings.Replace(goodPayload, `"ptoVta":2`, `"ptoVta":0`, 1)),
		"currency length": qrURL(strings.Replace(goodPayload, "PES", "PESO", 1)),
	}
	for name, u := range bad {
		if q, err := ParseQRURL(u); err == nil {
			t.Errorf("%s: accepted %+v", name, q)
		}
	}
}

func TestParseQRURLEncodings(t *testing.T) {
	// URL safe base64 without padding is accepted too.
	urlSafe := "https://www.afip.gob.ar/fe/qr?p=" + strings.TrimRight(
		strings.NewReplacer("+", "-", "/", "_").Replace(strings.TrimPrefix(qrURL(goodPayload), "https://www.arca.gob.ar/fe/qr/?p=")), "=")
	for _, u := range []string{qrURL(goodPayload), urlSafe} {
		q, err := ParseQRURL(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		if q.Total != 12100050 || q.IssuerCUIT != issuer {
			t.Errorf("total %s cuit %s", q.Total, q.IssuerCUIT)
		}
	}
}

func extract(t *testing.T, e *Extractor, in Input) *Invoice {
	t.Helper()
	inv, err := e.Extract(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func wantFields(t *testing.T, inv *Invoice, src Source) {
	t.Helper()
	want := map[Name]string{IssuerCUIT: issuer, DocType: "1", PointOfSale: "2", Number: "1234",
		IssueDate: "2026-11-05", Total: "121000.50", AuthCode: "76451234567890"}
	for n, v := range want {
		f, ok := inv.Get(n)
		if !ok || f.Value != v {
			t.Errorf("%s = %q; want %q (trail %v)", n, f.Value, v, inv.Trail)
			continue
		}
		if f.Source != src {
			t.Errorf("%s from %s; want %s", n, f.Source, src)
		}
		if f.Conflict != nil {
			t.Errorf("%s in conflict with %q from %s", n, f.Conflict.Value, f.Conflict.Source)
		}
	}
}

// Spec, Documents: an invoice with a QR fills CUIT, type, number, amount and
// CAE without a model. The QR is found wherever the PDF carries it.
func TestQRFirstInEveryCarrier(t *testing.T) {
	img := qrImage(t, qrURL(goodPayload), 300)
	cases := map[string]Input{
		"flate image": {PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Image: &pdfImage{Gray: img.Pix, W: 300, H: 300}})},
		"jpeg image":  {PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Image: &pdfImage{JPEG: jpegOf(t, img), W: 300, H: 300}})},
		"link":        {PDF: buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload)})},
	}
	var pngBuf bytes.Buffer
	png.Encode(&pngBuf, img)
	cases["photo"] = Input{Image: pngBuf.Bytes()}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			inv := extract(t, &Extractor{OwnCUIT: receiver}, in)
			wantFields(t, inv, SourceQR)
			if !inv.AutoEligible() {
				t.Errorf("untrusted %v (trail %v)", inv.Untrusted(), inv.Trail)
			}
			if inv.Trail[0] != "qr: valid" {
				t.Errorf("trail %v", inv.Trail)
			}
		})
	}
}

func TestTextLayerWithoutQR(t *testing.T) {
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines})})
	wantFields(t, inv, SourcePDFText)
	if f, _ := inv.Get(AuthDue); f.Value != "2026-11-15" {
		t.Errorf("CAE due %q", f.Value)
	}
	if !inv.AutoEligible() {
		t.Errorf("untrusted %v", inv.Untrusted())
	}
}

// Without knowing the tenant's own CUIT the issuer is ambiguous: low
// confidence, so it cannot feed an automatic match.
func TestTwoCUITsWithoutOwnCUIT(t *testing.T) {
	inv := extract(t, &Extractor{}, Input{PDF: buildPDF(t, pdfSpec{Lines: invoiceLines})})
	f, _ := inv.Get(IssuerCUIT)
	if f.Confidence >= AutoThreshold || inv.AutoEligible() {
		t.Errorf("issuer %+v trusted with two candidate CUITs", f)
	}
}

func TestTextDisagreesWithQR(t *testing.T) {
	lines := append([]string{}, invoiceLines...)
	lines[9] = "Importe Total: $ 112.000,50"
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: buildPDF(t, pdfSpec{Lines: lines, Link: qrURL(goodPayload)})})
	f, _ := inv.Get(Total)
	if f.Value != "121000.50" || f.Source != SourceQR || f.Conflict == nil || f.Conflict.Value != "112000.50" {
		t.Fatalf("total %+v", f)
	}
	if inv.AutoEligible() || inv.MinConfidence() > LLMCap {
		t.Error("a field in conflict must keep the invoice out of automatic")
	}
}

func TestTypeCodeAndLetterDisagree(t *testing.T) {
	lines := append([]string{}, invoiceLines...)
	lines[0] = "FACTURA B"
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: buildPDF(t, pdfSpec{Lines: lines})})
	if f, _ := inv.Get(DocType); f.Trusted() {
		t.Errorf("doc type %+v trusted though code and letter disagree", f)
	}
}

func TestTypeFromLetterOnly(t *testing.T) {
	lines := append([]string{}, invoiceLines...)
	lines[1] = ""
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: buildPDF(t, pdfSpec{Lines: lines})})
	if f, _ := inv.Get(DocType); f.Value != "1" || f.Confidence != confDerived {
		t.Errorf("doc type %+v", f)
	}
}

func TestTwoDifferentQRsUseNone(t *testing.T) {
	second := strings.Replace(goodPayload, `"nroCmp":1234`, `"nroCmp":1235`, 1)
	img := qrImage(t, qrURL(second), 300)
	pdfb := buildPDF(t, pdfSpec{Lines: invoiceLines, Link: qrURL(goodPayload), Image: &pdfImage{Gray: img.Pix, W: 300, H: 300}})
	inv := extract(t, &Extractor{OwnCUIT: receiver}, Input{PDF: pdfb})
	if f, _ := inv.Get(Number); f.Source == SourceQR {
		t.Errorf("used a QR although the document carries two different ones: %v", inv.Trail)
	}
}

type fakeOCR struct{ text string }

func (f fakeOCR) Text(context.Context, []byte) (string, error) { return f.text, nil }

type fakeLLM struct {
	vals  map[Name]string
	asked []Name
}

func (f *fakeLLM) Fields(_ context.Context, _ string, missing []Name) (map[Name]string, error) {
	f.asked = missing
	return f.vals, nil
}

// A scan with no text layer and no readable QR goes to OCR; OCR values sit
// below the automatic threshold until OCR precision is measured.
func TestScannedGoesToOCR(t *testing.T) {
	scan := buildPDF(t, pdfSpec{})
	inv := extract(t, &Extractor{OwnCUIT: receiver, OCR: fakeOCR{strings.Join(invoiceLines, "\n")}}, Input{PDF: scan})
	wantFields(t, inv, SourceOCR)
	if inv.AutoEligible() {
		t.Error("OCR values must not be trusted yet")
	}
}

// The model fills only what is missing, its values are validated, and an
// invoice with any model value never qualifies for automatic.
func TestLLMLastAndNeverTrusted(t *testing.T) {
	lines := []string{"FACTURA A", "COD. 01", "CUIT: 30-00000001-5", "Fecha de Emisión: 05/11/2026", "Importe Total: $ 121.000,50"}
	llm := &fakeLLM{vals: map[Name]string{
		PointOfSale: "0002", Number: "00001234", AuthCode: "76451234567890",
		IssuerCUIT: "30000000031", // already read from text: must be ignored
	}}
	inv := extract(t, &Extractor{OwnCUIT: receiver, LLM: llm}, Input{PDF: buildPDF(t, pdfSpec{Lines: lines})})
	if len(llm.asked) != 3 {
		t.Errorf("model asked for %v; want only the three missing fields", llm.asked)
	}
	if f, _ := inv.Get(IssuerCUIT); f.Value != issuer || f.Source != SourcePDFText || f.Conflict != nil {
		t.Errorf("issuer %+v", f)
	}
	for _, n := range []Name{PointOfSale, Number, AuthCode} {
		f, ok := inv.Get(n)
		if !ok || f.Source != SourceLLM || f.Confidence != LLMCap || f.Trusted() {
			t.Errorf("%s = %+v", n, f)
		}
	}
	if inv.AutoEligible() || inv.MinConfidence() > LLMCap {
		t.Error("an invoice with model values must go to review")
	}
}

func TestLLMInvalidValuesDropped(t *testing.T) {
	llm := &fakeLLM{vals: map[Name]string{IssuerCUIT: "30-00000001-6", AuthCode: "123", Total: "mucho", IssueDate: "ayer"}}
	inv := extract(t, &Extractor{LLM: llm}, Input{PDF: buildPDF(t, pdfSpec{Lines: []string{"hola"}})})
	for _, n := range []Name{IssuerCUIT, AuthCode, Total, IssueDate} {
		if _, ok := inv.Get(n); ok {
			t.Errorf("%s accepted from an invalid model value", n)
		}
	}
}

func TestMalformedPDF(t *testing.T) {
	for _, b := range [][]byte{[]byte("not a pdf"), []byte("%PDF-1.4\n1 0 obj\n<<")} {
		if _, err := (&Extractor{}).Extract(context.Background(), Input{PDF: b}); err == nil {
			t.Errorf("%q: no error", b)
		}
	}
	if _, err := (&Extractor{}).Extract(context.Background(), Input{}); err == nil {
		t.Error("empty input accepted")
	}
}

func TestImageWithoutQR(t *testing.T) {
	white := image.NewGray(image.Rect(0, 0, 100, 100))
	for i := range white.Pix {
		white.Pix[i] = 255
	}
	inv := extract(t, &Extractor{}, Input{Image: jpegOf(t, white)})
	if len(inv.Fields) != 0 || !strings.Contains(strings.Join(inv.Trail, ";"), "qr: none found") {
		t.Errorf("fields %v trail %v", inv.Fields, inv.Trail)
	}
	if _, err := DecodeQRImage([]byte("garbage")); err == nil {
		t.Error("garbage decoded")
	}
}
