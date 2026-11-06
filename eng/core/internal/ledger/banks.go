package ledger

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// The layouts below were written from synthetic samples: none has been
// checked against a real export yet (Verified: false). The menus of the home
// banking sites could not be inspected (product/PRODUCTO.md, "Sin
// verificar"). When the first real file of a bank arrives it either matches
// the fingerprint, passes the balance check and the layout is marked
// verified, or it lands in quarantine and the layout is fixed from it.
// Mercado Pago follows the documented release report (RECORD_TYPE values
// initial_available_balance, release and total); its column set and the
// meaning of the total row still need a real report to confirm.

func init() {
	register(&Layout{
		ID:        "galicia/csv-v1",
		Bank:      Galicia,
		Delimiter: ';',
		Header: []string{"Fecha", "Descripción", "Origen", "Débitos", "Créditos", "Grupo de Conceptos",
			"Concepto", "Número de Terminal", "Observaciones Cliente", "Número de Comprobante",
			"Leyendas Adicionales 1", "Leyendas Adicionales 2", "Tipo de Movimiento", "Saldo"},
		parse: columnar(columns{Date: 0, Desc: 1, Ref: 9, Debit: 3, Credit: 4, Signed: -1, Balance: 13,
			Style: money.DecimalComma, NewestFirst: true}),
	})
	register(&Layout{
		ID:        "santander/csv-v1",
		Bank:      Santander,
		Delimiter: ';',
		Header:    []string{"Fecha", "Sucursal origen", "Descripción", "Referencia", "Importe", "Saldo"},
		parse:     parseSantander,
	})
	register(&Layout{
		ID:        "bancor/csv-v1",
		Bank:      Bancor,
		Delimiter: ',',
		Header:    []string{"Fecha", "Concepto", "Comprobante", "Débito", "Crédito", "Saldo"},
		parse: columnar(columns{Date: 0, Desc: 1, Ref: 2, Debit: 3, Credit: 4, Signed: -1, Balance: 5,
			Style: money.DecimalComma}),
	})
	register(&Layout{
		ID:        "macro/txt-v1",
		Bank:      Macro,
		Delimiter: '\t',
		Header:    []string{"Fecha", "Nro. Comprobante", "Descripción", "Importe", "Saldo"},
		parse: columnar(columns{Date: 0, Desc: 2, Ref: 1, Debit: -1, Credit: -1, Signed: 3, Balance: 4,
			Style: money.DecimalComma}),
	})
	register(&Layout{
		ID:        "mercadopago/release-v1",
		Bank:      MercadoPago,
		Delimiter: ',',
		Header: []string{"DATE", "SOURCE_ID", "EXTERNAL_REFERENCE", "RECORD_TYPE", "DESCRIPTION",
			"NET_CREDIT_AMOUNT", "NET_DEBIT_AMOUNT", "GROSS_AMOUNT", "MP_FEE_AMOUNT", "FINANCING_FEE_AMOUNT",
			"SHIPPING_FEE_AMOUNT", "TAXES_AMOUNT", "COUPON_AMOUNT", "INSTALLMENTS", "PAYMENT_METHOD"},
		parse: parseMercadoPago,
	})
}

// columnar is the parser for layouts that only carry a running balance.
func columnar(c columns) func(*sheet, *Layout) (*Statement, error) {
	return func(s *sheet, l *Layout) (*Statement, error) {
		mv, rest, err := parseColumns(s, c)
		if err != nil {
			return nil, err
		}
		for _, r := range rest {
			if !r.blank() {
				return nil, &RowError{r.Line, "", errors.New("unexpected content after the data")}
			}
		}
		st := &Statement{Movements: mv}
		finish(st, c.Balance >= 0)
		return st, nil
	}
}

// parseSantander reads a preamble with the account, period and opening
// balance, the movements, and a trailer with the closing balance. A file
// without its trailer is incomplete and does not enter.
func parseSantander(s *sheet, l *Layout) (*Statement, error) {
	st := &Statement{}
	for _, r := range s.Rows[:s.Header] {
		switch normalizeHeader(r.cell(0)) {
		case "cuenta":
			st.AccountNumber = r.cell(1)
		case "periodo":
			from, err1 := parseDate(r.cell(1))
			to, err2 := parseDate(r.cell(2))
			if err := errors.Join(err1, err2); err != nil {
				return nil, &RowError{r.Line, "Período", err}
			}
			st.From, st.To = from, to
		case "saldo inicial":
			a, err := money.Parse(r.cell(1), money.DecimalComma)
			if err != nil {
				return nil, &RowError{r.Line, "Saldo inicial", err}
			}
			st.Opening, st.OpeningStated = a, true
		}
	}
	mv, rest, err := parseColumns(s, columns{Date: 0, Desc: 2, Ref: 3, Debit: -1, Credit: -1, Signed: 4,
		Balance: 5, Style: money.DecimalComma, StopAtLabel: []string{"saldo final"}})
	if err != nil {
		return nil, err
	}
	for _, r := range rest {
		if normalizeHeader(r.cell(0)) == "saldo final" {
			a, err := money.Parse(r.cell(1), money.DecimalComma)
			if err != nil {
				return nil, &RowError{r.Line, "Saldo final", err}
			}
			st.Closing, st.ClosingStated = a, true
		}
	}
	if !st.OpeningStated || !st.ClosingStated {
		return nil, ErrIncomplete
	}
	from, to := st.From, st.To
	st.Movements = mv
	finish(st, true)
	if !from.IsZero() {
		for _, m := range mv {
			if m.Date.Before(from) || m.Date.After(to) {
				return nil, &RowError{m.Line, "Fecha", fmt.Errorf("outside the stated period")}
			}
		}
		st.From, st.To = from, to
	}
	return st, nil
}

// ErrIncomplete means the file lacks a part its layout always carries
// (opening or closing balance, total row): it was cut or exported partially.
var ErrIncomplete = errors.New("incomplete file: stated balances missing")

var mpFeeColumns = []struct {
	col  int
	kind string
}{{8, "mp_fee"}, {9, "financing_fee"}, {10, "shipping_fee"}, {11, "taxes"}, {12, "coupon"}}

// parseMercadoPago reads the release report: one initial_available_balance
// row (opening), release rows (movements, with gross and documented fees)
// and one total row (closing). For each release with a gross amount the
// gross plus the fees has to equal the net, so a fee is never inferred.
func parseMercadoPago(s *sheet, l *Layout) (*Statement, error) {
	st := &Statement{}
	header := s.Rows[s.Header].Cells
	for _, r := range s.Rows[s.Header+1:] {
		if r.blank() {
			continue
		}
		if len(r.Cells) < len(header) {
			return nil, &RowError{r.Line, "", fmt.Errorf("%d cells, header has %d", len(r.Cells), len(header))}
		}
		credit, err := optional(r.cell(5), money.DecimalDot)
		if err != nil {
			return nil, &RowError{r.Line, header[5], err}
		}
		debit, err := optional(r.cell(6), money.DecimalDot)
		if err != nil {
			return nil, &RowError{r.Line, header[6], err}
		}
		net := credit - debit
		switch strings.ToLower(r.cell(3)) {
		case "initial_available_balance":
			if st.OpeningStated {
				return nil, &RowError{r.Line, header[3], errors.New("second opening row")}
			}
			st.Opening, st.OpeningStated = net, true
		case "total":
			if st.ClosingStated {
				return nil, &RowError{r.Line, header[3], errors.New("second total row")}
			}
			st.Closing, st.ClosingStated = net, true
		case "release":
			if st.ClosingStated {
				return nil, &RowError{r.Line, header[3], errors.New("movement after the total row")}
			}
			d, err := parseDate(r.cell(0))
			if err != nil {
				return nil, &RowError{r.Line, header[0], err}
			}
			m := Movement{Line: r.Line, Date: d, Reference: r.cell(1), ExternalRef: r.cell(2),
				Description: r.cell(4), Amount: net}
			if r.cell(7) != "" {
				g, err := money.Parse(r.cell(7), money.DecimalDot)
				if err != nil {
					return nil, &RowError{r.Line, header[7], err}
				}
				sum := g
				for _, f := range mpFeeColumns {
					a, err := optional(r.cell(f.col), money.DecimalDot)
					if err != nil {
						return nil, &RowError{r.Line, header[f.col], err}
					}
					if a != 0 {
						m.Fees = append(m.Fees, Fee{Kind: f.kind, Amount: a})
						sum += a
					}
				}
				if sum != net {
					return nil, &RowError{r.Line, header[7], fmt.Errorf("gross plus fees is %s, net is %s", sum, net)}
				}
				m.Gross = &g
			}
			st.Movements = append(st.Movements, m)
		default:
			return nil, &RowError{r.Line, header[3], fmt.Errorf("record type %q not known", r.cell(3))}
		}
	}
	if !st.OpeningStated || !st.ClosingStated {
		return nil, ErrIncomplete
	}
	slices.SortStableFunc(st.Movements, func(a, b Movement) int { return a.Date.Compare(b.Date) })
	finish(st, false)
	return st, nil
}
