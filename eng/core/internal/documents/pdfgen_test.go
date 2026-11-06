package documents

// A minimal PDF writer for synthetic test invoices: one page, Helvetica with
// WinAnsi encoding, text lines, an optional image XObject and an optional
// link annotation. Fixtures are generated at test time so no binary and no
// real invoice is ever committed.

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

type pdfImage struct {
	Gray []byte // 8 bit gray pixels, used when JPEG is nil
	W, H int
	JPEG []byte
}

type pdfSpec struct {
	Lines []string // top to bottom
	Image *pdfImage
	Link  string // URI of a link annotation
}

func latin1(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 256 {
			b.WriteByte(byte(r))
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}

func pdfEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(latin1(s))
}

func buildPDF(t *testing.T, spec pdfSpec) []byte {
	t.Helper()
	var content bytes.Buffer
	y := 800
	for _, l := range spec.Lines {
		fmt.Fprintf(&content, "BT /F1 10 Tf 40 %d Td (%s) Tj ET\n", y, pdfEscape(l))
		y -= 14
	}
	if spec.Image != nil {
		content.WriteString("q 120 0 0 120 420 40 cm /Im1 Do Q\n")
	}

	var objs []string
	add := func(s string) int { objs = append(objs, s); return len(objs) }
	stream := func(dict string, data []byte) string {
		return fmt.Sprintf("<< %s /Length %d >>\nstream\n%s\nendstream", dict, len(data), data)
	}

	catalog := add("") // placeholder, filled below
	pages := add("")
	font := add("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	cont := add(stream("", content.Bytes()))
	xobj := ""
	if im := spec.Image; im != nil {
		var id int
		if im.JPEG != nil {
			id = add(stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode", im.W, im.H), im.JPEG))
		} else {
			var z bytes.Buffer
			zw := zlib.NewWriter(&z)
			zw.Write(im.Gray)
			zw.Close()
			id = add(stream(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", im.W, im.H), z.Bytes()))
		}
		xobj = fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", id)
	}
	annots := ""
	if spec.Link != "" {
		a := add(fmt.Sprintf("<< /Type /Annot /Subtype /Link /Rect [420 40 540 160] /Border [0 0 0] /A << /S /URI /URI (%s) >> >>", spec.Link))
		annots = fmt.Sprintf(" /Annots [%d 0 R]", a)
	}
	page := add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 %d 0 R >>%s >> /Contents %d 0 R%s >>",
		pages, font, xobj, cont, annots))
	objs[catalog-1] = fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages)
	objs[pages-1] = fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page)

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, catalog, xref)
	return out.Bytes()
}

// qrImage renders text as a QR in 8 bit gray.
func qrImage(t *testing.T, text string, size int) *image.Gray {
	t.Helper()
	m, err := qrcode.NewQRCodeWriter().Encode(text, gozxing.BarcodeFormat_QR_CODE, size, size, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := image.NewGray(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !m.Get(x, y) {
				g.Pix[y*size+x] = 255
			}
		}
	}
	return g
}

func jpegOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// qrURL builds an ARCA QR url from a JSON payload.
func qrURL(payload string) string {
	return "https://www.arca.gob.ar/fe/qr/?p=" + base64.StdEncoding.EncodeToString([]byte(payload))
}
