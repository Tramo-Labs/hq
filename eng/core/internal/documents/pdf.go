package documents

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// pdfDoc is what the extractor needs from a PDF: its text layer and the
// images it embeds (where the QR usually is).
type pdfDoc struct {
	Text   string
	Images [][]byte // each one PNG or JPEG encoded, ready for DecodeQRImage
}

// readPDF parses a PDF. The parser panics on some malformed files; that is
// turned into an error so one bad upload never takes the worker down.
func readPDF(data []byte) (doc *pdfDoc, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, fmt.Errorf("pdf: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("pdf: %w", err)
	}
	doc = &pdfDoc{}
	var lines []string
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			return nil, fmt.Errorf("pdf page %d: %w", i, err)
		}
		for _, row := range rows {
			lines = append(lines, joinRow(row.Content))
		}
		doc.Images = append(doc.Images, pageImages(p)...)
	}
	doc.Text = strings.Join(lines, "\n")
	doc.Images = append(doc.Images, rawJPEGs(data)...)
	return doc, nil
}

// joinRow glues the text pieces of a row, adding a space where there is a
// visible gap between the end of one piece and the start of the next.
func joinRow(ts pdf.TextHorizontal) string {
	var b strings.Builder
	for i, t := range ts {
		if i > 0 {
			prev := ts[i-1]
			gap := t.X - (prev.X + prev.W)
			if prev.W == 0 || gap > 1.0 {
				if !strings.HasSuffix(b.String(), " ") && !strings.HasPrefix(t.S, " ") {
					b.WriteByte(' ')
				}
			}
		}
		b.WriteString(t.S)
	}
	return strings.TrimSpace(b.String())
}

// pageImages returns the image XObjects of a page that the parser can
// decode itself (FlateDecode or no filter; 1 or 8 bits; gray or RGB),
// re-encoded as PNG. JPEG images are found by rawJPEGs instead.
func pageImages(p pdf.Page) [][]byte {
	var out [][]byte
	xo := p.Resources().Key("XObject")
	for _, k := range xo.Keys() {
		v := xo.Key(k)
		if v.Key("Subtype").Name() != "Image" {
			continue
		}
		if img, err := xobjectImage(v); err == nil {
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err == nil {
				out = append(out, buf.Bytes())
			}
		}
	}
	return out
}

func xobjectImage(v pdf.Value) (img image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("image: %v", r)
		}
	}()
	switch f := v.Key("Filter"); f.Kind() {
	case pdf.Null:
	case pdf.Name:
		if f.Name() != "FlateDecode" {
			return nil, errors.New("image filter not supported: " + f.Name())
		}
	default:
		return nil, errors.New("image filter chain not supported")
	}
	w, h := int(v.Key("Width").Int64()), int(v.Key("Height").Int64())
	bpc := int(v.Key("BitsPerComponent").Int64())
	cs := v.Key("ColorSpace").Name()
	if w <= 0 || h <= 0 || w > 10000 || h > 10000 {
		return nil, errors.New("image size out of range")
	}
	rc := v.Reader()
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, int64(w)*int64(h)*3+1))
	if err != nil {
		return nil, err
	}
	switch {
	case cs == "DeviceGray" && bpc == 8 && len(raw) >= w*h:
		g := image.NewGray(image.Rect(0, 0, w, h))
		copy(g.Pix, raw[:w*h])
		return g, nil
	case cs == "DeviceGray" && bpc == 1:
		stride := (w + 7) / 8
		if len(raw) < stride*h {
			return nil, errors.New("short image data")
		}
		g := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if raw[y*stride+x/8]&(0x80>>(x%8)) != 0 {
					g.SetGray(x, y, color.Gray{Y: 255})
				}
			}
		}
		return g, nil
	case cs == "DeviceRGB" && bpc == 8 && len(raw) >= w*h*3:
		m := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := 0; i < w*h; i++ {
			m.Pix[i*4], m.Pix[i*4+1], m.Pix[i*4+2], m.Pix[i*4+3] = raw[i*3], raw[i*3+1], raw[i*3+2], 255
		}
		return m, nil
	}
	return nil, fmt.Errorf("image format not supported: %s %d bits", cs, bpc)
}

// rawJPEGs finds JPEG images stored as-is in the file (DCTDecode streams are
// not re-encoded by PDF writers). Each candidate only matters if it decodes
// to a QR that then validates, so a false hit costs nothing.
func rawJPEGs(data []byte) [][]byte {
	var out [][]byte
	soi := []byte{0xff, 0xd8, 0xff}
	eoi := []byte{0xff, 0xd9}
	for i := 0; i < len(data) && len(out) < 20; {
		s := bytes.Index(data[i:], soi)
		if s < 0 {
			break
		}
		s += i
		e := bytes.Index(data[s:], []byte("endstream"))
		if e < 0 {
			break
		}
		seg := data[s : s+e]
		if j := bytes.LastIndex(seg, eoi); j >= 0 {
			out = append(out, seg[:j+2])
		}
		i = s + e
	}
	return out
}
