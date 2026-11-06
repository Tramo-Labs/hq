package ledger

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
)

// zipOf writes the named parts, in order, into a zip.
func zipOf(t testing.TB, parts ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range parts {
		w, err := zw.Create(p[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(p[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bookHead returns the workbook and relations parts for n sheets.
func bookHead(n int) [][2]string {
	var sh, rel strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sh, `<sheet name="S%d" sheetId="%d" r:id="rId%d"/>`, i, i, i)
		fmt.Fprintf(&rel, `<Relationship Id="rId%d" Target="worksheets/sheet%d.xml"/>`, i, i)
	}
	return [][2]string{
		{"xl/workbook.xml", `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` +
			sh.String() + `</sheets></workbook>`},
		{"xl/_rels/workbook.xml.rels", `<Relationships>` + rel.String() + `</Relationships>`},
	}
}

// hostileBook builds a workbook whose sheets expand far beyond what they
// weigh: each sheet is rowXML repeated until it reaches size bytes.
func hostileBook(t testing.TB, sheets int, size int, rowXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range bookHead(sheets) {
		w, _ := zw.Create(p[0])
		w.Write([]byte(p[1]))
	}
	head, tail := `<worksheet><sheetData>`, `</sheetData></worksheet>`
	n := (size - len(head) - len(tail)) / len(rowXML)
	chunk := []byte(strings.Repeat(rowXML, 1024))
	for i := 1; i <= sheets; i++ {
		w, err := zw.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i))
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(head))
		for j := 0; j < n/1024; j++ {
			w.Write(chunk)
		}
		w.Write([]byte(tail))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// peakRSS returns the process's peak resident memory in bytes, or 0 where
// /proc is not there. resetPeakRSS starts a new peak (Linux 4.0 and later).
func peakRSS() uint64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "VmHWM:" {
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			return kb << 10
		}
	}
	return 0
}

func resetPeakRSS() { _ = os.WriteFile("/proc/self/clear_refs", []byte("5"), 0) }

type measurement struct {
	reason, detail string
	alloc          uint64 // bytes allocated during Parse, garbage included
	grew           uint64 // growth of the peak resident memory during Parse
	took           time.Duration
}

// measure runs Parse with the heap returned to the system first, so the
// peak growth is what this one file cost.
func measure(data []byte) measurement {
	debug.FreeOSMemory()
	resetPeakRSS()
	base := peakRSS()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	var m measurement
	_, m.reason, m.detail = Parse(data)
	m.took = time.Since(start)
	runtime.ReadMemStats(&after)
	m.alloc = after.TotalAlloc - before.TotalAlloc
	if p := peakRSS(); p > base {
		m.grew = p - base
	}
	return m
}

// Hostile workbooks, each under the upload cap, are quarantined as
// unsupported without being read whole, under a memory and time ceiling.
// TRAMO_XLSX_MEASURE=1 logs what each one cost.
func TestHostileWorkbooks(t *testing.T) {
	if testing.Short() {
		t.Skip("builds large workbooks")
	}
	inline := `<row><c t="inlineStr"><is><t>x</t></is></c></row>`
	cell := `<c><v>1</v></c>`
	sheetOf := func(body string) string { return `<worksheet><sheetData>` + body + `</sheetData></worksheet>` }

	// Each file is built inside its own subtest, so the test holds one at a
	// time and stays well under the memory cap it runs with.
	cases := []struct {
		name  string
		build func() []byte
		limit string // the limit that must stop it
	}{
		{"ratio 256 to 1", func() []byte { return hostileBook(t, 1, 64<<20, inline) }, "200000 rows"},
		{"50 sheets of 8 MiB", func() []byte { return hostileBook(t, 50, 8<<20, inline) }, "decompressed size"},
		// Rows with no cells cost nothing to keep, so only the bytes stop them.
		{"empty rows", func() []byte { return hostileBook(t, 1, 80<<20, `<row/>`) }, "decompressed size"},
		{"rows padded to column XFD", func() []byte {
			return hostileBook(t, 1, 4<<20, `<row><c r="XFD1"><v>1</v></c></row>`)
		}, "1000000 cells"},
		{"many rows", func() []byte { return hostileBook(t, 1, 12<<20, `<row><c><v>1</v></c></row>`) }, "200000 rows"},
		// Rows of 200 short cells: no part is large, the cells are many.
		{"many cells", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml",
				sheetOf(strings.Repeat(`<row>`+strings.Repeat(cell, 200)+`</row>`, 8_000))})...)
		}, "1000000 cells"},
		// One cell of 40 MiB of text.
		{"one huge cell", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml",
				sheetOf(`<row><c t="inlineStr"><is><t>` + strings.Repeat("x", 40<<20) + `</t></is></c></row>`)})...)
		}, "one XML token"},
		// Two million shared strings, referenced by nobody.
		{"many shared strings", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml", sheetOf("")},
				[2]string{"xl/sharedStrings.xml", `<sst>` + strings.Repeat(`<si><t>x</t></si>`, 2_000_000) + `</sst>`})...)
		}, "shared strings"},
		// A million sheet entries in the workbook part.
		{"many sheet entries", func() []byte {
			return zipOf(t, [2]string{"xl/workbook.xml", `<workbook><sheets>` +
				strings.Repeat(`<sheet r:id="x"/>`, 1_000_000) + `</sheets></workbook>`})
		}, "50 sheets"},
		// Styles with a million cell formats.
		{"many styles", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml", sheetOf("")},
				[2]string{"xl/styles.xml", `<styleSheet><cellXfs>` + strings.Repeat(`<xf numFmtId="14"/>`, 1_000_000) +
					`</cellXfs></styleSheet>`})...)
		}, "cell formats"},
		// Ten thousand custom number formats.
		{"many number formats", func() []byte {
			var nf strings.Builder
			for i := 0; i < 10_000; i++ {
				fmt.Fprintf(&nf, `<numFmt numFmtId="%d" formatCode="0.00"/>`, 200+i)
			}
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml", sheetOf("")},
				[2]string{"xl/styles.xml", `<styleSheet><numFmts>` + nf.String() + `</numFmts></styleSheet>`})...)
		}, "number formats"},
		// Five thousand tiny parts: the directory is small (about 260 KiB),
		// the entries are many.
		{"many zip entries", func() []byte {
			many := append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml", sheetOf("")})
			for i := 0; i < 5_000; i++ {
				many = append(many, [2]string{fmt.Sprintf("x/%d", i), ""})
			}
			return zipOf(t, many...)
		}, "zip entries"},
		// Nine hundred parts, under the entry limit, with names long enough
		// to make the directory larger than 1 MiB.
		{"large zip directory", func() []byte {
			long := append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml", sheetOf("")})
			for i := 0; i < 900; i++ {
				long = append(long, [2]string{fmt.Sprintf("x/%04d/%s", i, strings.Repeat("n", 1200)), ""})
			}
			return zipOf(t, long...)
		}, "zip directory size"},
		{"deep nesting", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml",
				strings.Repeat("<a>", 100_000) + strings.Repeat("</a>", 100_000)})...)
		}, "element nesting"},
		{"long run of text across many tokens", func() []byte {
			return zipOf(t, append(bookHead(1), [2]string{"xl/worksheets/sheet1.xml",
				sheetOf(`<row><c t="inlineStr"><is>` + strings.Repeat(`<r><t>`+strings.Repeat("x", 1000)+`</t></r>`, 200) +
					`</is></c></row>`)})...)
		}, "text in one cell"},
	}

	// The hard ceiling is the peak resident memory: that is what can take
	// the server down. Allocation counts garbage the collector hands back
	// (the XML decoder makes a little per token), so its ceiling only
	// catches a reader that went back to holding parts whole. Under the race
	// detector memory and time are several times larger and not measured.
	const maxAlloc, maxGrowth = 1 << 30, 96 << 20
	report := os.Getenv("TRAMO_XLSX_MEASURE") != ""
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := c.build()
			if len(data) > MaxUpload {
				t.Fatalf("hostile file is %d bytes, over the upload cap: not a real threat", len(data))
			}
			m := measure(data)
			if report {
				t.Logf("zip %.2f MiB: %s (%s); allocated %d MiB, peak RSS +%d MiB, %v",
					float64(len(data))/(1<<20), m.reason, m.detail, m.alloc>>20, m.grew>>20,
					m.took.Round(time.Millisecond))
			}
			if m.reason != ReasonUnsupportedFormat {
				t.Errorf("reason %s (%s); want %s", m.reason, m.detail, ReasonUnsupportedFormat)
			}
			if !strings.Contains(m.detail, "over the limit of "+c.limit) {
				t.Errorf("detail %q; want the limit of %s", m.detail, c.limit)
			}
			if raceEnabled {
				return
			}
			if m.alloc > maxAlloc {
				t.Errorf("allocated %d MiB; ceiling %d MiB", m.alloc>>20, maxAlloc>>20)
			}
			if m.grew > maxGrowth {
				t.Errorf("peak RSS grew %d MiB; ceiling %d MiB", m.grew>>20, maxGrowth>>20)
			}
			if m.took > 5*time.Second {
				t.Errorf("took %v", m.took)
			}
		})
	}
}

// The limits leave room for a real export: a year of a busy account, with
// long descriptions as shared strings, is read whole (its layout is not
// known, so it lands in unknown layout, never in unsupported format).
func TestLargeRealisticWorkbookIsRead(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a large workbook")
	}
	const rows = 30_000
	var ws, ss strings.Builder
	ws.WriteString(`<worksheet><sheetData><row r="1">` +
		`<c r="A1" t="inlineStr"><is><t>Fecha</t></is></c><c r="B1" t="inlineStr"><is><t>Concepto</t></is></c>` +
		`<c r="C1" t="inlineStr"><is><t>Referencia</t></is></c><c r="D1" t="inlineStr"><is><t>Debito</t></is></c>` +
		`<c r="E1" t="inlineStr"><is><t>Credito</t></is></c><c r="F1" t="inlineStr"><is><t>Saldo</t></is></c></row>`)
	ss.WriteString(`<sst>`)
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&ss, `<si><t>TRANSFERENCIA RECIBIDA CUIT 30-7%08d-1 PROVEEDOR DE PRUEBA SA %d</t></si>`, i, i)
		r := i + 2
		fmt.Fprintf(&ws, `<row r="%d"><c r="A%d" s="1"><v>%d</v></c><c r="B%d" t="s"><v>%d</v></c>`+
			`<c r="C%d"><v>%d</v></c><c r="D%d"><v>0</v></c><c r="E%d"><v>1234.5</v></c><c r="F%d"><v>%d.5</v></c></row>`,
			r, r, 45658+i/100, r, i, r, 900000+i, r, r, r, 1234*(i+1))
	}
	ws.WriteString(`</sheetData></worksheet>`)
	ss.WriteString(`</sst>`)
	parts := append(bookHead(1),
		[2]string{"xl/worksheets/sheet1.xml", ws.String()},
		[2]string{"xl/sharedStrings.xml", ss.String()},
		[2]string{"xl/styles.xml", `<styleSheet><cellXfs><xf numFmtId="0"/><xf numFmtId="14"/></cellXfs></styleSheet>`})
	data := zipOf(t, parts...)
	m := measure(data)
	if os.Getenv("TRAMO_XLSX_MEASURE") != "" {
		t.Logf("zip %.2f MiB, xml %.1f MiB: %s; allocated %d MiB, peak RSS +%d MiB, %v",
			float64(len(data))/(1<<20), float64(ws.Len()+ss.Len())/(1<<20), m.reason,
			m.alloc>>20, m.grew>>20, m.took.Round(time.Millisecond))
	}
	if m.reason != ReasonUnknownLayout {
		t.Errorf("reason %s (%s); want %s", m.reason, m.detail, ReasonUnknownLayout)
	}
}
