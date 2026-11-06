package portal

import (
	"encoding/csv"
	"strings"
	"testing"
)

// Bank descriptions are written by third parties: a cell that a spreadsheet
// would run as a formula is neutralized; amounts, negatives included, stay
// numbers; the header row is never touched.
func TestCSVNeutralizesFormulas(t *testing.T) {
	out, err := writeCSV([][]string{
		{"=header", "amount"},
		{"=HYPERLINK(\"http://x\",\"pay\")", "-1234.56"},
		{"+54 351 555", "@SUM(A1)"},
		{"-cmd|' /C calc'!A0", "\tTAB"},
		{"TRANSF. RECIBIDA", "1000.00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"=header", "amount"},
		{"'=HYPERLINK(\"http://x\",\"pay\")", "-1234.56"},
		{"'+54 351 555", "'@SUM(A1)"},
		{"'-cmd|' /C calc'!A0", "'\tTAB"},
		{"TRANSF. RECIBIDA", "1000.00"},
	}
	for i := range want {
		for j := range want[i] {
			if rows[i][j] != want[i][j] {
				t.Errorf("row %d col %d: %q, want %q", i, j, rows[i][j], want[i][j])
			}
		}
	}
}
