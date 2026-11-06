package documents

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // decoders for scanned or photographed invoices
	_ "image/png"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// QR is the content of the ARCA invoice QR, version 1
// (afip.gob.ar/fe/qr/documentos/QRespecificaciones.pdf, text in
// eng/arca/manuales/qr.txt).
type QR struct {
	Version      int
	Date         time.Time
	IssuerCUIT   string // 11 digits
	PointOfSale  int
	DocType      int
	Number       int64
	Total        money.Amount
	Currency     string
	Rate         string
	ReceiverType int    // 0 when absent
	ReceiverDoc  string // empty when absent
	AuthType     string // E or A
	AuthCode     string // 14 digits
}

var qrHosts = map[string]bool{
	"www.arca.gob.ar": true, "arca.gob.ar": true,
	"www.afip.gob.ar": true, "afip.gob.ar": true,
}

// QRURLPattern finds a QR URL inside the text or the raw bytes of a PDF
// (link annotations usually carry it next to the image).
var QRURLPattern = regexp.MustCompile(`https?://(?:www\.)?(?:afip|arca)\.gob\.ar/fe/qr/?\?p=[A-Za-z0-9+/=_%-]+`)

// ParseQRURL decodes and validates the text of an ARCA invoice QR. Any
// field out of the specification rejects the whole QR: a QR that does not
// validate is not a source.
func ParseQRURL(raw string) (*QR, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("qr: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" || !qrHosts[strings.ToLower(u.Host)] ||
		strings.TrimSuffix(u.Path, "/") != "/fe/qr" {
		return nil, errors.New("qr: not an ARCA invoice QR url")
	}
	// The payload is standard base64 and may contain '+': read it with path
	// unescaping, which keeps '+', instead of query unescaping, which turns
	// it into a space.
	var p string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		if v, ok := strings.CutPrefix(kv, "p="); ok {
			if p, err = url.PathUnescape(v); err != nil {
				return nil, fmt.Errorf("qr: %w", err)
			}
		}
	}
	if p == "" {
		return nil, errors.New("qr: missing p parameter")
	}
	payload, err := decodeBase64(p)
	if err != nil {
		return nil, fmt.Errorf("qr: payload is not base64: %w", err)
	}
	return parseQRJSON(payload)
}

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}

type qrJSON struct {
	Ver        json.Number `json:"ver"`
	Fecha      string      `json:"fecha"`
	Cuit       json.Number `json:"cuit"`
	PtoVta     json.Number `json:"ptoVta"`
	TipoCmp    json.Number `json:"tipoCmp"`
	NroCmp     json.Number `json:"nroCmp"`
	Importe    json.Number `json:"importe"`
	Moneda     string      `json:"moneda"`
	Ctz        json.Number `json:"ctz"`
	TipoDocRec json.Number `json:"tipoDocRec"`
	NroDocRec  json.Number `json:"nroDocRec"`
	TipoCodAut string      `json:"tipoCodAut"`
	CodAut     json.Number `json:"codAut"`
}

var (
	reDigits = regexp.MustCompile(`^[0-9]+$`)
	reRate   = regexp.MustCompile(`^[0-9]{1,13}(\.[0-9]{1,6})?$`)
)

func parseQRJSON(b []byte) (*QR, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var j qrJSON
	if err := dec.Decode(&j); err != nil {
		return nil, fmt.Errorf("qr: json: %w", err)
	}
	var errs []error
	bad := func(field, why string) { errs = append(errs, fmt.Errorf("%s %s", field, why)) }
	intField := func(name string, n json.Number, maxDigits int, required bool) int64 {
		s := n.String()
		if s == "" {
			if required {
				bad(name, "missing")
			}
			return 0
		}
		if !reDigits.MatchString(s) || len(s) > maxDigits {
			bad(name, "is not an integer of up to "+strconv.Itoa(maxDigits)+" digits")
			return 0
		}
		v, _ := strconv.ParseInt(s, 10, 64)
		return v
	}
	q := &QR{}
	q.Version = int(intField("ver", j.Ver, 1, true))
	if q.Version != 1 && len(errs) == 0 {
		bad("ver", "is not 1")
	}
	if d, err := time.Parse("2006-01-02", j.Fecha); err != nil {
		bad("fecha", "is not YYYY-MM-DD")
	} else {
		q.Date = d
	}
	cuit := j.Cuit.String()
	if !ValidCUIT(cuit) {
		bad("cuit", "is not a valid CUIT")
	}
	q.IssuerCUIT = cuit
	q.PointOfSale = int(intField("ptoVta", j.PtoVta, 5, true))
	q.DocType = int(intField("tipoCmp", j.TipoCmp, 3, true))
	q.Number = intField("nroCmp", j.NroCmp, 8, true)
	if q.PointOfSale == 0 || q.DocType == 0 || q.Number == 0 {
		if len(errs) == 0 {
			bad("ptoVta, tipoCmp or nroCmp", "is zero")
		}
	}
	if imp := j.Importe.String(); strings.ContainsAny(imp, "eE") {
		bad("importe", "uses an exponent")
	} else if a, err := money.Parse(imp, money.DecimalDot); err != nil || a < 0 || strings.Contains(imp, ",") {
		bad("importe", "is not a decimal with up to two places")
	} else {
		q.Total = a
	}
	if len(j.Moneda) != 3 {
		bad("moneda", "is not a three letter code")
	}
	q.Currency = j.Moneda
	if !reRate.MatchString(j.Ctz.String()) {
		bad("ctz", "is not a decimal with up to six places")
	}
	q.Rate = j.Ctz.String()
	q.ReceiverType = int(intField("tipoDocRec", j.TipoDocRec, 2, false))
	if s := j.NroDocRec.String(); s != "" {
		if !reDigits.MatchString(s) || len(s) > 20 {
			bad("nroDocRec", "is not up to 20 digits")
		}
		q.ReceiverDoc = s
	}
	if j.TipoCodAut != "E" && j.TipoCodAut != "A" {
		bad("tipoCodAut", "is not E or A")
	}
	q.AuthType = j.TipoCodAut
	if s := j.CodAut.String(); !reDigits.MatchString(s) || len(s) != 14 {
		bad("codAut", "is not 14 digits")
	} else {
		q.AuthCode = s
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("qr: %w", errors.Join(errs...))
	}
	return q, nil
}

// ValidCUIT checks length, prefix-free digits and the mod 11 check digit.
func ValidCUIT(s string) bool {
	if len(s) != 11 || !reDigits.MatchString(s) {
		return false
	}
	w := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, m := range w {
		sum += int(s[i]-'0') * m
	}
	dv := 11 - sum%11
	switch dv {
	case 11:
		dv = 0
	case 10:
		return false
	}
	return int(s[10]-'0') == dv
}

// qrConfidence is the confidence of a field read from a QR that validated.
// It is not 1: the QR says what the issuer declared, and WSCDC is what
// confirms ARCA authorized it.
const qrConfidence = 0.98

// Fields turns a validated QR into invoice fields.
func (q *QR) Fields() map[Name]Field {
	f := func(v string) Field { return Field{Value: v, Source: SourceQR, Confidence: qrConfidence} }
	out := map[Name]Field{
		IssuerCUIT:  f(q.IssuerCUIT),
		DocType:     f(strconv.Itoa(q.DocType)),
		PointOfSale: f(strconv.Itoa(q.PointOfSale)),
		Number:      f(strconv.FormatInt(q.Number, 10)),
		IssueDate:   f(q.Date.Format("2006-01-02")),
		Total:       f(q.Total.String()),
		Currency:    f(q.Currency),
		Rate:        f(q.Rate),
		AuthType:    f(q.AuthType),
		AuthCode:    f(q.AuthCode),
	}
	if q.ReceiverType != 0 {
		out[ReceiverType] = f(strconv.Itoa(q.ReceiverType))
	}
	if q.ReceiverDoc != "" {
		out[ReceiverDoc] = f(q.ReceiverDoc)
	}
	return out
}

// DecodeQRImage reads a QR from an image (PNG or JPEG bytes) and returns
// its text. It does not validate it; ParseQRURL does.
func DecodeQRImage(data []byte) (string, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("qr image: %w", err)
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", fmt.Errorf("qr image: %w", err)
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", fmt.Errorf("qr image: %w", err)
	}
	return res.GetText(), nil
}
