package ledger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ingest(t *testing.T, in *Ingester, bank Bank, account, name string) Result {
	t.Helper()
	r, err := in.Ingest(context.Background(), Upload{TenantID: "t1", AccountID: account, Bank: bank,
		Filename: name, Data: fixture(t, name)})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The five formats of task 4 are read from synthetic fixtures and close.
func TestParseEveryBank(t *testing.T) {
	cases := []struct {
		file     string
		bank     Bank
		layout   string
		n        int
		opening  money.Amount
		closing  money.Amount
		credits  money.Amount
		stated   bool
		firstRef string
	}{
		{"galicia_ok.csv", Galicia, "galicia/csv-v1", 10, 100000000, 166443473, 80525075, false, "000101"},
		{"santander_ok.csv", Santander, "santander/csv-v1", 10, 100000000, 166443473, 80525075, true, "000101"},
		{"bancor_ok.csv", Bancor, "bancor/csv-v1", 10, 100000000, 166443473, 80525075, false, "000101"},
		{"macro_ok.txt", Macro, "macro/txt-v1", 10, 100000000, 166443473, 80525075, false, "000101"},
		{"mercadopago_ok.csv", MercadoPago, "mercadopago/release-v1", 4, 500000, 1619449, 4119449, true, "90000000001"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			st, reason, detail := Parse(fixture(t, c.file))
			if reason != "" {
				t.Fatalf("Parse: %s: %s", reason, detail)
			}
			if st.Bank != c.bank || st.LayoutID != c.layout {
				t.Errorf("layout %s/%s; want %s/%s", st.Bank, st.LayoutID, c.bank, c.layout)
			}
			if len(st.Movements) != c.n {
				t.Fatalf("%d movements; want %d", len(st.Movements), c.n)
			}
			if st.Opening != c.opening || st.Closing != c.closing || st.Credits() != c.credits {
				t.Errorf("opening %s closing %s credits %s; want %s %s %s",
					st.Opening, st.Closing, st.Credits(), c.opening, c.closing, c.credits)
			}
			if st.OpeningStated != c.stated || st.ClosingStated != c.stated {
				t.Errorf("stated balances %v/%v; want %v", st.OpeningStated, st.ClosingStated, c.stated)
			}
			if st.Movements[0].Reference != c.firstRef {
				t.Errorf("first movement %q; want %q (chronological order)", st.Movements[0].Reference, c.firstRef)
			}
			for i := 1; i < len(st.Movements); i++ {
				if st.Movements[i].Date.Before(st.Movements[i-1].Date) {
					t.Fatalf("movement %d out of chronological order", i)
				}
			}
			if err := CheckBalance(st); err != nil {
				t.Errorf("CheckBalance: %v", err)
			}
		})
	}
}

func TestGaliciaLatin1Description(t *testing.T) {
	st, reason, _ := Parse(fixture(t, "galicia_ok.csv"))
	if reason != "" {
		t.Fatal(reason)
	}
	if got := st.Movements[1].Description; got != "PAGO PROVEEDOR EJEMPLO SA" {
		t.Errorf("description %q", got)
	}
	if st.Movements[1].Amount != -12050050 {
		t.Errorf("debit parsed as %s", st.Movements[1].Amount)
	}
}

func TestMercadoPagoDocumentedFees(t *testing.T) {
	st, reason, detail := Parse(fixture(t, "mercadopago_ok.csv"))
	if reason != "" {
		t.Fatal(reason, detail)
	}
	m := st.Movements[0]
	if m.Gross == nil || *m.Gross != 1000000 || m.Amount != 947000 {
		t.Fatalf("gross %v net %s", m.Gross, m.Amount)
	}
	if len(m.Fees) != 2 || m.Fees[0].Kind != "mp_fee" || m.Fees[0].Amount != -41000 || m.Fees[1].Kind != "taxes" {
		t.Errorf("fees %+v", m.Fees)
	}
	if m.Reference != "90000000001" || m.ExternalRef != "PEDIDO-1001" {
		t.Errorf("refs %q %q", m.Reference, m.ExternalRef)
	}
	if w := st.Movements[3]; w.Amount != -3000000 || w.Gross != nil {
		t.Errorf("withdrawal %+v", w)
	}
}

// Every way a file can be wrong ends in quarantine with its reason, and
// nothing of it is stored.
func TestQuarantine(t *testing.T) {
	cases := []struct {
		file   string
		bank   Bank
		reason string
	}{
		{"galicia_changed_layout.csv", Galicia, ReasonUnknownLayout},
		{"santander_truncated.csv", Santander, ReasonIncomplete},
		{"bancor_bad_balance.csv", Bancor, ReasonBalance},
		{"macro_bad_amount.txt", Macro, ReasonRowParse},
		{"mercadopago_fee_mismatch.csv", MercadoPago, ReasonRowParse},
		{"mercadopago_no_total.csv", MercadoPago, ReasonIncomplete},
		{"bancor_ok.csv", Galicia, ReasonBankMismatch},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			s := NewMemStore()
			r := ingest(t, &Ingester{Store: s}, c.bank, "acc", c.file)
			if r.Status != Quarantined || r.Reason != c.reason {
				t.Fatalf("status %s reason %s (%s); want quarantined %s", r.Status, r.Reason, r.Detail, c.reason)
			}
			if len(s.Movements("t1", "acc")) != 0 || len(s.Quarantine) != 1 {
				t.Errorf("quarantined file left movements or no quarantine record")
			}
		})
	}
}

func TestChangedLayoutReportsFingerprint(t *testing.T) {
	r := ingest(t, &Ingester{Store: NewMemStore()}, Galicia, "acc", "galicia_changed_layout.csv")
	var galicia *Layout
	for _, l := range Layouts() {
		if l.ID == "galicia/csv-v1" {
			galicia = l
		}
	}
	if r.Fingerprint == "" || r.Fingerprint == galicia.Fingerprint() {
		t.Errorf("fingerprint %q should be the new header's, not the registered one", r.Fingerprint)
	}
	if !strings.Contains(r.Detail, r.Fingerprint) {
		t.Errorf("detail %q does not name the fingerprint", r.Detail)
	}
}

func TestBalanceErrorNamesTheLine(t *testing.T) {
	r := ingest(t, &Ingester{Store: NewMemStore()}, Bancor, "acc", "bancor_bad_balance.csv")
	if !strings.Contains(r.Detail, "line 6") {
		t.Errorf("detail %q should point at line 6", r.Detail)
	}
}

// F9: the same bytes uploaded to an account of another bank are refused as
// a bank mismatch, not reported as a duplicate the client would read as
// "already loaded".
func TestBankMismatchBeforeDuplicate(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	if r := ingest(t, in, Galicia, "galicia-acc", "galicia_ok.csv"); r.Status != Accepted {
		t.Fatalf("first upload %+v", r)
	}
	r := ingest(t, in, Santander, "santander-acc", "galicia_ok.csv")
	if r.Status != Quarantined || r.Reason != ReasonBankMismatch {
		t.Fatalf("same file to a santander account: %s %s (%s)", r.Status, r.Reason, r.Detail)
	}
	if r := ingest(t, in, Galicia, "galicia-acc", "galicia_ok.csv"); r.Status != Duplicate {
		t.Errorf("same file, same bank: %s", r.Status)
	}
}

// Duotach case 4: re-importing the same file creates nothing.
func TestReimportSameFile(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	first := ingest(t, in, Bancor, "acc", "bancor_ok.csv")
	if first.Status != Accepted || first.New != 10 {
		t.Fatalf("first upload %s new %d", first.Status, first.New)
	}
	again := ingest(t, in, Bancor, "acc", "bancor_ok.csv")
	if again.Status != Duplicate || again.New != 0 {
		t.Fatalf("second upload %s new %d", again.Status, again.New)
	}
	if n := len(s.Movements("t1", "acc")); n != 10 {
		t.Errorf("%d movements stored; want 10", n)
	}
}

// Overlapping exports: the shared day enters once; the genuine twin
// transfers of that day stay two movements.
func TestOverlappingExports(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	p1 := ingest(t, in, Bancor, "acc", "bancor_part1.csv")
	p2 := ingest(t, in, Bancor, "acc", "bancor_part2.csv")
	if p1.Status != Accepted || p2.Status != Accepted {
		t.Fatalf("%s %s (%s)", p1.Status, p2.Status, p2.Detail)
	}
	if p1.New != 7 || p2.New != 3 || p2.AlreadyPresent != 3 {
		t.Errorf("part1 new %d, part2 new %d already %d; want 7, 3, 3", p1.New, p2.New, p2.AlreadyPresent)
	}
	if n := len(s.Movements("t1", "acc")); n != 10 {
		t.Errorf("%d movements stored; want 10", n)
	}
}

func TestChainWithPreviousStatement(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	ingest(t, in, Bancor, "acc", "bancor_ok.csv")
	if r := ingest(t, in, Bancor, "acc", "bancor_nov_gap.csv"); r.Reason != ReasonChainGap {
		t.Fatalf("gap: %s %s", r.Status, r.Reason)
	}
	if r := ingest(t, in, Bancor, "acc", "bancor_nov.csv"); r.Status != Accepted {
		t.Fatalf("november: %s %s %s", r.Status, r.Reason, r.Detail)
	}
}

func TestAccountNumberMismatch(t *testing.T) {
	s := NewMemStore()
	r, err := (&Ingester{Store: s}).Ingest(context.Background(), Upload{TenantID: "t1", AccountID: "acc",
		Bank: Santander, AccountNumber: "000-000099/9", Data: fixture(t, "santander_ok.csv")})
	if err != nil || r.Reason != ReasonAccountMismatch {
		t.Fatalf("%v %s", err, r.Reason)
	}
	r, err = (&Ingester{Store: s}).Ingest(context.Background(), Upload{TenantID: "t1", AccountID: "acc",
		Bank: Santander, AccountNumber: "000-000001/0", Data: fixture(t, "santander_ok.csv")})
	if err != nil || r.Status != Accepted {
		t.Fatalf("%v %s %s", err, r.Status, r.Reason)
	}
}

// A quarantined file can be uploaded again once its layout is fixed: its
// hash is not burned.
func TestQuarantineDoesNotBurnTheHash(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	ingest(t, in, Galicia, "acc", "galicia_changed_layout.csv")
	if r := ingest(t, in, Galicia, "acc", "galicia_changed_layout.csv"); r.Status != Quarantined {
		t.Errorf("second try %s; want quarantined again, not duplicate", r.Status)
	}
}

func TestMovementKeysStable(t *testing.T) {
	st, _, _ := Parse(fixture(t, "macro_ok.txt"))
	a := movementKeys("acc", st.Movements)
	b := movementKeys("acc", st.Movements)
	seen := map[string]bool{}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("keys not deterministic")
		}
		if seen[a[i]] {
			t.Fatalf("duplicate key at %d", i)
		}
		seen[a[i]] = true
	}
	if other := movementKeys("acc2", st.Movements); other[0] == a[0] {
		t.Error("key does not depend on the account")
	}
}

// F3: the same movement with another description (an edited cell, a bank
// that rewrote the concept) is never stored twice. The file goes to
// quarantine naming the line; a change of case or spacing alone is the
// same movement.
func TestDescriptionIsNotPartOfTheIdentity(t *testing.T) {
	s := NewMemStore()
	in := &Ingester{Store: s}
	if r := ingest(t, in, Galicia, "acc", "galicia_ok.csv"); r.Status != Accepted || r.New != 10 {
		t.Fatalf("first upload %s new %d", r.Status, r.New)
	}
	upload := func(data []byte) Result {
		t.Helper()
		r, err := in.Ingest(context.Background(), Upload{TenantID: "t1", AccountID: "acc", Bank: Galicia, Data: data})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	orig := string(fixture(t, "galicia_ok.csv"))
	for _, desc := range []string{"=1+1 CLIENTE UNO", "@SUM(A1) CLIENTE UNO", "TRANSFERENCIA RECIBIDA CLIENTE TRES"} {
		r := upload([]byte(strings.Replace(orig, "TRANSFERENCIA RECIBIDA CLIENTE UNO", desc, 1)))
		if r.Status != Quarantined || r.Reason != ReasonMovementConflict || !strings.Contains(r.Detail, "line 11") {
			t.Errorf("%q: %s %s %q", desc, r.Status, r.Reason, r.Detail)
		}
		if strings.Contains(r.Detail, "CLIENTE") || strings.Contains(r.Detail, "250") {
			t.Errorf("detail carries data from the file: %q", r.Detail)
		}
	}
	r := upload([]byte(strings.Replace(orig, "TRANSFERENCIA RECIBIDA CLIENTE UNO", "Transferencia  recibida cliente uno", 1)))
	if r.Status != Accepted || r.New != 0 || r.AlreadyPresent != 10 {
		t.Errorf("cosmetic change: %s new %d already %d (%s)", r.Status, r.New, r.AlreadyPresent, r.Detail)
	}
	if n := len(s.Movements("t1", "acc")); n != 10 {
		t.Errorf("%d movements stored; want 10", n)
	}
}

// F16: a line the CSV reader cannot split (a stray or unclosed quote) is
// row_parse naming the line, not an unknown layout with an empty
// fingerprint; the detail carries nothing from the cell.
func TestBrokenQuoteNamesTheLine(t *testing.T) {
	cases := []struct {
		file, from, to string
		bank           Bank
		line           string
	}{
		{"galicia_ok.csv", "TRANSFERENCIA RECIBIDA CLIENTE UNO", `=HYPERLINK("http://x","a")`, Galicia, "line 11:"},
		{"bancor_ok.csv", `"TRANSFERENCIA RECIBIDA CLIENTE UNO"`, `"TRANSFERENCIA "RECIBIDA" CLIENTE UNO"`, Bancor, "line 2:"},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			data := strings.Replace(string(fixture(t, c.file)), c.from, c.to, 1)
			st, reason, detail := Parse([]byte(data))
			if st != nil || reason != ReasonRowParse || !strings.HasPrefix(detail, c.line) {
				t.Fatalf("%s %q; want row_parse at %s", reason, detail, c.line)
			}
			if strings.Contains(detail, "HYPERLINK") || strings.Contains(detail, "RECIBIDA") {
				t.Errorf("detail carries the cell: %q", detail)
			}
			r, err := (&Ingester{Store: NewMemStore()}).Ingest(context.Background(),
				Upload{TenantID: "t1", AccountID: "acc", Bank: c.bank, Data: []byte(data)})
			if err != nil || r.Status != Quarantined || r.Reason != ReasonRowParse {
				t.Errorf("ingest: %v %s %s", err, r.Status, r.Reason)
			}
		})
	}
}

// Identical movements without a running balance still get distinct keys.
func TestTwinsWithoutBalance(t *testing.T) {
	m := Movement{Description: "X", Amount: 100}
	k := movementKeys("acc", []Movement{m, m})
	if k[0] == k[1] {
		t.Error("twins share a key")
	}
}

func TestFingerprintIgnoresCosmetics(t *testing.T) {
	a := Fingerprint(';', []string{"Fecha", "Descripción", "Saldo"})
	b := Fingerprint(';', []string{" FECHA ", "Descripcion", "saldo", ""})
	if a != b {
		t.Error("accent, case and trailing empty column should not change the fingerprint")
	}
	if a == Fingerprint(',', []string{"Fecha", "Descripción", "Saldo"}) {
		t.Error("delimiter must be part of the fingerprint")
	}
	if a == Fingerprint(';', []string{"Fecha", "Saldo", "Descripción"}) {
		t.Error("column order must be part of the fingerprint")
	}
}

func TestNoBalanceEvidence(t *testing.T) {
	st := &Statement{Movements: []Movement{{Amount: 100}}}
	if err := CheckBalance(st); err == nil {
		t.Error("a statement without balances must not pass")
	}
}
