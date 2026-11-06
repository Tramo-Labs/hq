package documents

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// ItemDraft is the open item an invoice generates for matching: a
// receivable when the tenant issued it, a payable when the tenant received
// it. Documents derives it; matching stores it (see PGStore.OnSaved).
type ItemDraft struct {
	Kind       string // receivable or payable
	Date       time.Time
	Amount     money.Amount // positive receivable, negative payable; credit notes inverted
	Refs       []string
	Party      string // the CUIT on the other side
	Confidence float64
	// DirectionCertain is true only when the direction comes from the ARCA
	// QR (issuer, and receiver when the tenant is the receiver).
	DirectionCertain bool
}

// Reasons an invoice generates no item. Shown in "para revisar".
const (
	NoItemTenantCUIT  = "no_tenant_cuit"        // the tenant has no CUIT on file: the side cannot be told
	NoItemTotalOrDate = "missing_total_or_date" // no trusted total or issue date
	NoItemCurrency    = "foreign_currency"      // not pesos: no conversion is guessed
	NoItemDirection   = "unknown_direction"     // no issuer to tell which side the tenant is on
	// NoItemNotThisTenant: issuer and receiver are both trusted and neither
	// is the tenant. The invoice is someone else's; it is never eligible
	// for automatic (F21).
	NoItemNotThisTenant = "not_this_tenant"
)

// DirectionCap is the confidence of an item whose direction is not
// certain: below the automatic threshold, so it can only be suggested.
const DirectionCap = LLMCap

// creditNotes are the ARCA comprobante codes of credit notes: they reverse
// the sign of the item.
var creditNotes = map[string]bool{"3": true, "8": true, "13": true, "53": true}

// DraftItem derives the open item of an invoice for the tenant whose CUIT
// is ownCUIT, or says why there is none.
//
// The direction is certain only from the QR: the QR issuer is the tenant
// (receivable), or the QR receiver is the tenant (payable). From the text
// layer it is not: the text rules exclude the tenant's own CUIT from the
// issuer candidates, so an invoice the tenant issued, read without QR,
// would look like one it received. An uncertain direction keeps the item
// below the automatic threshold.
func DraftItem(inv *Invoice, ownCUIT string) (ItemDraft, string) {
	if ownCUIT == "" {
		return ItemDraft{}, NoItemTenantCUIT
	}
	total, okTotal := inv.Get(Total)
	date, okDate := inv.Get(IssueDate)
	if !okTotal || !okDate || !total.Trusted() || !date.Trusted() {
		return ItemDraft{}, NoItemTotalOrDate
	}
	if cur, ok := inv.Get(Currency); ok && cur.Value != "PES" {
		return ItemDraft{}, NoItemCurrency
	}
	issuer, ok := inv.Get(IssuerCUIT)
	if !ok {
		return ItemDraft{}, NoItemDirection
	}
	if rcv, ok := inv.Get(ReceiverDoc); ok && issuer.Trusted() && rcv.Trusted() &&
		issuer.Value != ownCUIT && rcv.Value != ownCUIT {
		return ItemDraft{}, NoItemNotThisTenant
	}
	amount, err := money.Parse(total.Value, money.DecimalDot)
	if err != nil {
		return ItemDraft{}, NoItemTotalOrDate
	}
	d, err := time.Parse("2006-01-02", date.Value)
	if err != nil {
		return ItemDraft{}, NoItemTotalOrDate
	}

	it := ItemDraft{Date: d, Confidence: inv.MinConfidence()}
	receiver, hasReceiver := inv.Get(ReceiverDoc)
	switch {
	case issuer.Value == ownCUIT:
		it.Kind, it.Amount = "receivable", amount
		if hasReceiver {
			it.Party = receiver.Value
		}
		it.DirectionCertain = issuer.Source == SourceQR
	default:
		it.Kind, it.Amount, it.Party = "payable", -amount, issuer.Value
		it.DirectionCertain = issuer.Source == SourceQR && hasReceiver &&
			receiver.Source == SourceQR && receiver.Value == ownCUIT
	}
	if dt, ok := inv.Get(DocType); ok && creditNotes[dt.Value] {
		it.Amount = -it.Amount
	}
	if !it.DirectionCertain {
		it.Confidence = min(it.Confidence, DirectionCap)
	}
	if pos, ok := inv.Get(PointOfSale); ok {
		if num, ok := inv.Get(Number); ok {
			p, errP := strconv.Atoi(pos.Value)
			n, errN := strconv.ParseInt(num.Value, 10, 64)
			if errP == nil && errN == nil {
				it.Refs = []string{fmt.Sprintf("%04d-%08d", p, n), fmt.Sprintf("%08d", n)}
			}
		}
	}
	return it, ""
}
