// Package ledger turns bank and Mercado Pago exports into movements that can
// be trusted. A file enters whole or not at all: the layout has to match a
// known fingerprint, every row has to parse, the balances have to close and
// the file and each movement are deduplicated before anything is stored.
// Anything else is quarantined with a reason for an operator.
package ledger

import (
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Bank identifies the institution that produced an export.
type Bank string

// Banks with a parser today.
const (
	Galicia     Bank = "galicia"
	Santander   Bank = "santander"
	Bancor      Bank = "bancor"
	Macro       Bank = "macro"
	MercadoPago Bank = "mercadopago"
)

// Fee is a deduction the source itself documents for a movement, such as
// the Mercado Pago commission or a tax withheld on a settlement.
type Fee struct {
	Kind   string       // mp_fee, financing_fee, shipping_fee, taxes, coupon
	Amount money.Amount // signed as it affects the net: deductions are negative
}

// Movement is one line of a statement, in chronological order.
type Movement struct {
	Line        int       // 1-based line of the source file
	Date        time.Time // civil date, UTC midnight
	Description string
	Reference   string        // bank voucher number, Mercado Pago SOURCE_ID
	ExternalRef string        // reference the account holder put on it, if any
	Amount      money.Amount  // credits positive, debits negative
	Balance     *money.Amount // running balance after this line, when the file has it
	Gross       *money.Amount // gross amount, when the source documents fees
	Fees        []Fee
	Key         string // deduplication key, set by Ingest
}

// Statement is a parsed export, already normalized to chronological order.
type Statement struct {
	LayoutID      string
	Bank          Bank
	Fingerprint   string
	AccountNumber string // when the file states it
	Currency      string
	From, To      time.Time
	Opening       money.Amount
	Closing       money.Amount
	// OpeningStated and ClosingStated say whether the file states the
	// balance or it was derived from the running balance column.
	OpeningStated bool
	ClosingStated bool
	Running       bool // every movement carries its running balance
	Movements     []Movement
}

// Credits returns the total of credits.
func (s *Statement) Credits() money.Amount {
	var t money.Amount
	for _, m := range s.Movements {
		if m.Amount > 0 {
			t += m.Amount
		}
	}
	return t
}

// Debits returns the total of debits as a positive amount.
func (s *Statement) Debits() money.Amount {
	var t money.Amount
	for _, m := range s.Movements {
		if m.Amount < 0 {
			t -= m.Amount
		}
	}
	return t
}

func civil(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
