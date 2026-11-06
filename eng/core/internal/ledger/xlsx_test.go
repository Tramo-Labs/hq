package ledger

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// The Excel export carries the same movements as the CSV one: same
// balances, same order, same fields, and so the same movement keys. A
// tenant that uploads October as CSV and then as Excel stores it once.
func TestWorkbookMatchesCSV(t *testing.T) {
	csv, reason, detail := Parse(fixture(t, "bancor_ok.csv"))
	if reason != "" {
		t.Fatalf("csv: %s %s", reason, detail)
	}
	for _, file := range []string{"bancor_ok.xlsx", "bancor_two_sheets.xlsx"} {
		t.Run(file, func(t *testing.T) {
			st, reason, detail := Parse(fixture(t, file))
			if reason != "" {
				t.Fatalf("%s: %s", reason, detail)
			}
			if st.LayoutID != "bancor/xlsx-v1" || st.Bank != Bancor {
				t.Errorf("layout %s %s", st.Bank, st.LayoutID)
			}
			if st.Opening != csv.Opening || st.Closing != csv.Closing || st.Credits() != csv.Credits() ||
				!st.From.Equal(csv.From) || !st.To.Equal(csv.To) || !st.Running {
				t.Errorf("opening %s closing %s credits %s %s..%s; want %s %s %s %s..%s",
					st.Opening, st.Closing, st.Credits(), day(st.From), day(st.To),
					csv.Opening, csv.Closing, csv.Credits(), day(csv.From), day(csv.To))
			}
			if len(st.Movements) != len(csv.Movements) {
				t.Fatalf("%d movements; want %d", len(st.Movements), len(csv.Movements))
			}
			for i, m := range st.Movements {
				c := csv.Movements[i]
				if !m.Date.Equal(c.Date) || m.Amount != c.Amount || *m.Balance != *c.Balance ||
					m.Reference != c.Reference || m.Description != c.Description {
					t.Errorf("movement %d: %s %s %s %q %q; want %s %s %s %q %q", i, day(m.Date), m.Amount, *m.Balance,
						m.Reference, m.Description, day(c.Date), c.Amount, *c.Balance, c.Reference, c.Description)
				}
				if m.Sheet == 0 {
					t.Errorf("movement %d has no sheet", i)
				}
			}
			if err := CheckBalance(st); err != nil {
				t.Error(err)
			}
		})
	}

	s := NewMemStore()
	in := &Ingester{Store: s}
	if r := ingest(t, in, Bancor, "acc", "bancor_ok.csv"); r.Status != Accepted {
		t.Fatalf("csv: %s %s", r.Status, r.Detail)
	}
	r := ingest(t, in, Bancor, "acc", "bancor_ok.xlsx")
	if r.Status != Accepted || r.New != 0 || r.AlreadyPresent != 10 {
		t.Errorf("xlsx after csv: %s new %d already %d (%s); want accepted 0 10", r.Status, r.New, r.AlreadyPresent, r.Detail)
	}
	if n := len(s.Movements("t1", "acc")); n != 10 {
		t.Errorf("%d movements stored; want 10", n)
	}
}

// Sheets are put in date order whatever their position in the workbook: in
// bancor_two_sheets the first half is the second sheet, typed as text.
func TestWorkbookSheetsInDateOrder(t *testing.T) {
	st, reason, detail := Parse(fixture(t, "bancor_two_sheets.xlsx"))
	if reason != "" {
		t.Fatalf("%s: %s", reason, detail)
	}
	if st.Movements[0].Sheet != 2 || st.Movements[len(st.Movements)-1].Sheet != 1 {
		t.Errorf("first movement from sheet %d, last from sheet %d; want 2 and 1",
			st.Movements[0].Sheet, st.Movements[len(st.Movements)-1].Sheet)
	}
}

func TestWorkbookQuarantine(t *testing.T) {
	cases := []struct {
		file   string
		reason string
		detail []string
	}{
		{"bancor_sheets_gap.xlsx", ReasonBalance,
			[]string{"sheet 2 opens on 06/10/2026", "sheet 1 closed on 06/10/2026", "difference 100.00"}},
		{"bancor_changed_layout.xlsx", ReasonUnknownLayout, []string{"sheet 1: header fingerprint"}},
		{"bancor_stray_sheet.xlsx", ReasonUnsupportedFormat, []string{"sheet 2", "held whole"}},
		{"bancor_bad_decimals.xlsx", ReasonRowParse, []string{"sheet 1, line 5", "F5", "more than two decimals"}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			s := NewMemStore()
			r := ingest(t, &Ingester{Store: s}, Bancor, "acc", c.file)
			if r.Status != Quarantined || r.Reason != c.reason {
				t.Fatalf("%s %s (%s); want quarantined %s", r.Status, r.Reason, r.Detail, c.reason)
			}
			for _, want := range c.detail {
				if !strings.Contains(r.Detail, want) {
					t.Errorf("detail %q does not say %q", r.Detail, want)
				}
			}
			if len(s.Movements("t1", "acc")) != 0 || len(s.Quarantine) != 1 {
				t.Errorf("quarantined file left movements or no quarantine record")
			}
		})
	}
}

// The fingerprint of an unknown sheet is the one reported, so the operator
// can register the layout from it; the detail never names cell contents.
func TestWorkbookUnknownLayoutFingerprint(t *testing.T) {
	r := ingest(t, &Ingester{Store: NewMemStore()}, Bancor, "acc", "bancor_changed_layout.xlsx")
	if r.Fingerprint == "" || !strings.Contains(r.Detail, r.Fingerprint) {
		t.Errorf("fingerprint %q, detail %q", r.Fingerprint, r.Detail)
	}
	if strings.Contains(r.Detail, "Sucursal") || strings.Contains(r.Detail, "TRANSFERENCIA") {
		t.Errorf("detail %q carries cell contents", r.Detail)
	}
}

func TestUnsupportedContainers(t *testing.T) {
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("notes.txt")
	w.Write([]byte("not a workbook"))
	zw.Close()

	var bomb bytes.Buffer
	zw = zip.NewWriter(&bomb)
	w, _ = zw.Create("xl/workbook.xml")
	w.Write(make([]byte, xlsxMaxXML+1))
	zw.Close()

	cases := map[string][]byte{
		"excel 97-2003": append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 504)...),
		"zip":           zipped.Bytes(),
		"oversized":     bomb.Bytes(),
		"cut workbook":  fixture(t, "bancor_ok.xlsx")[:1000],
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, reason, detail := Parse(data); reason != ReasonUnsupportedFormat {
				t.Errorf("%s (%s); want %s", reason, detail, ReasonUnsupportedFormat)
			}
		})
	}
}

func TestNumberCents(t *testing.T) {
	ok := map[string]int64{
		"1234.5": 123450, "-8500": -850000, "120500.50000000001": 12050050, "1129499.4999999998": 112949950,
		"1.5E3": 150000, "0": 0, "0.07": 7,
	}
	for in, want := range ok {
		if got, err := numberCents(in); err != nil || got != want {
			t.Errorf("numberCents(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"1234.567", "0.001", "abc", "", "1e30"} {
		if got, err := numberCents(in); err == nil {
			t.Errorf("numberCents(%q) = %d; want an error", in, got)
		}
	}
}

func TestSerialDate(t *testing.T) {
	for in, want := range map[string]string{
		"45658": "01/01/2025", "46304": "09/10/2026", "46304.75": "09/10/2026", "1": "01/01/1900",
		"59": "28/02/1900", "61": "01/03/1900",
	} {
		if got, err := serialDate(in, false); err != nil || got != want {
			t.Errorf("serialDate(%s) = %q, %v; want %s", in, got, err, want)
		}
	}
	if _, err := serialDate("0", false); err == nil {
		t.Error("serial 0 accepted")
	}
	if got, _ := serialDate("1", true); got != "02/01/1904" {
		t.Errorf("1904 system: %q", got)
	}
}

func TestIsDateFormat(t *testing.T) {
	for code, want := range map[string]bool{
		"dd/mm/yyyy": true, "d-mmm-yy": true, "#,##0.00": false, "[$-409]0.00": false,
		`"Saldo "0.00`: false, `0.00_);[Red](0.00)`: false, `[$-2C0A]dd/mm/yyyy;@`: true,
	} {
		if got := isDateFormat(code); got != want {
			t.Errorf("isDateFormat(%q) = %v; want %v", code, got, want)
		}
	}
}

// An Excel layout never collides with the CSV layout of the same columns.
func TestSheetFingerprintIsItsOwn(t *testing.T) {
	h := []string{"Fecha", "Concepto", "Comprobante", "Débito", "Crédito", "Saldo"}
	if Fingerprint(SheetDelimiter, h) == Fingerprint(',', h) {
		t.Error("xlsx and csv fingerprints collide")
	}
}
