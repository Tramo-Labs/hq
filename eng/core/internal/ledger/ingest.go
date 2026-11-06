package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tramo-Labs/hq/eng/core/internal/money"
)

// Status is what happened to an uploaded file.
type Status string

const (
	// Accepted: the file passed every check; its new movements are stored.
	Accepted Status = "accepted"
	// Duplicate: the exact same file was already uploaded for this tenant.
	Duplicate Status = "duplicate"
	// Quarantined: the file did not enter. Reason says why; an operator
	// has to look at it. Nothing of it reaches matching.
	Quarantined Status = "quarantined"
)

// Reason codes for a quarantine.
const (
	ReasonUnknownLayout   = "unknown_layout"   // header fingerprint not registered: new or changed format
	ReasonBankMismatch    = "bank_mismatch"    // layout of another bank than the account's
	ReasonAccountMismatch = "account_mismatch" // the file states another account number
	ReasonRowParse        = "row_parse"        // a row does not fit the layout
	ReasonIncomplete      = "incomplete"       // stated balances or total row missing
	ReasonBalance         = "balance_mismatch" // balances do not close
	ReasonChainGap        = "chain_gap"        // opening differs from the previous closing
	ReasonEmpty           = "empty"            // no movements and no stated balances
	// ReasonMovementConflict: a line has the identity of a stored movement
	// but another description. Never stored twice, never overwritten.
	ReasonMovementConflict = "movement_conflict"
	// ReasonUnsupportedFormat: Excel 97-2003, a zip that is not a
	// workbook, or a workbook whose sheets cannot be read as one statement
	// (different layouts, overlapping dates, movements under an unknown
	// header next to known sheets).
	ReasonUnsupportedFormat = "unsupported_format"
	// ReasonPeriodClosed: the file brings new movements dated in a closed
	// period. Nothing enters; Result.ClosedPeriod names the period.
	ReasonPeriodClosed = "period_closed"
)

// ClosedPeriod is a closed period of the tenant, [Start, End): End is the
// day after its last day.
type ClosedPeriod struct {
	Start, End time.Time
}

// PeriodClosedError is what a store's SaveAccepted returns when some new
// movement falls in a closed period: nothing was written.
type PeriodClosedError struct{ Period ClosedPeriod }

func (e *PeriodClosedError) Error() string {
	return fmt.Sprintf("ledger: new movements in the closed period from %s to %s",
		day(e.Period.Start), day(e.Period.End.AddDate(0, 0, -1)))
}

// Upload is a file a tenant sent for one of its accounts.
type Upload struct {
	TenantID      string
	AccountID     string
	Bank          Bank   // bank of the account
	AccountNumber string // as registered for the account; empty skips the check
	Filename      string
	Data          []byte
	ReceivedAt    time.Time
}

// Result is the outcome of Ingest.
type Result struct {
	Status         Status
	Reason         string // set when quarantined
	Detail         string // for the tenant only (may name the amounts that do not close); never logged nor audited
	FileHash       string
	Fingerprint    string
	LayoutID       string
	Statement      *Statement    // set when accepted
	New            int           // movements stored by this upload
	AlreadyPresent int           // movements skipped because an earlier upload had them
	ClosedPeriod   *ClosedPeriod // with ReasonPeriodClosed, the period the new movements hit
}

// Store persists files and movements. The Postgres implementation enforces
// the same uniqueness with constraints; MemStore is the reference used in
// tests.
type Store interface {
	// FileSeen reports whether the tenant already uploaded a file with this hash.
	FileSeen(ctx context.Context, tenantID, fileHash string) (bool, error)
	// MovementsSeen returns the keys the account already has, each with the
	// description stored for it.
	MovementsSeen(ctx context.Context, tenantID, accountID string, keys []string) (map[string]string, error)
	// LastStatement returns the latest accepted statement of the account, or nil.
	LastStatement(ctx context.Context, tenantID, accountID string) (*StatementSummary, error)
	// SaveAccepted stores the file, the statement and only the new movements, atomically.
	SaveAccepted(ctx context.Context, u Upload, r Result, fresh []Movement) error
	// SaveQuarantined keeps the file and the reason for an operator.
	SaveQuarantined(ctx context.Context, u Upload, r Result) error
}

// StatementSummary is what the chain check needs from the previous statement.
type StatementSummary struct {
	From, To time.Time
	Closing  money.Amount
}

// Ingester runs an upload through every check, in order.
type Ingester struct {
	Store Store
}

// Ingest checks and stores one file. The returned error is reserved for
// store failures; a bad file is a Result with Status Quarantined.
//
// Order: layout fingerprint and rows → bank and account → file hash
// (duplicate) → balances → chain with the previous statement → movement keys.
func (in *Ingester) Ingest(ctx context.Context, u Upload) (Result, error) {
	sum := sha256.Sum256(u.Data)
	res := Result{FileHash: hex.EncodeToString(sum[:])}
	quarantine := func(reason, detail string) (Result, error) {
		res.Status, res.Reason, res.Detail = Quarantined, reason, detail
		return res, in.Store.SaveQuarantined(ctx, u, res)
	}

	// What the file is comes first: a file of another bank or account is
	// refused as such even when the same bytes were accepted for another
	// account. Only then is it a duplicate of this tenant's upload.
	p := parse(u.Data)
	st, reason, detail := p.st, p.reason, p.detail
	if st != nil {
		res.Fingerprint, res.LayoutID = st.Fingerprint, st.LayoutID
	} else {
		res.Fingerprint = p.fingerprint
	}
	if reason != "" {
		return quarantine(reason, detail)
	}
	if reason, detail := identityChecks(u, st); reason != "" {
		return quarantine(reason, detail)
	}

	seen, err := in.Store.FileSeen(ctx, u.TenantID, res.FileHash)
	if err != nil {
		return res, err
	}
	if seen {
		return Result{Status: Duplicate, FileHash: res.FileHash, Fingerprint: res.Fingerprint, LayoutID: res.LayoutID,
			Detail: "this exact file was already uploaded"}, nil
	}

	if reason, detail, err = in.checks(ctx, u, st); err != nil {
		return res, err
	}
	if reason != "" {
		return quarantine(reason, detail)
	}

	keys := movementKeys(u.AccountID, st.Movements)
	have, err := in.Store.MovementsSeen(ctx, u.TenantID, u.AccountID, keys)
	if err != nil {
		return res, err
	}
	var fresh []Movement
	for i := range st.Movements {
		st.Movements[i].Key = keys[i]
		stored, ok := have[keys[i]]
		if !ok {
			fresh = append(fresh, st.Movements[i])
			continue
		}
		// Same account, date, amount, balance and references, other text:
		// a bank that rewrote the concept or an edited file. Either way the
		// file does not enter; it is never counted twice nor overwritten.
		if normalizeHeader(stored) != normalizeHeader(st.Movements[i].Description) {
			return quarantine(ReasonMovementConflict, fmt.Sprintf(
				"%s has the date, amount, balance and references of a movement already stored, with another description",
				where(st.Movements[i].Sheet, st.Movements[i].Line)))
		}
		res.AlreadyPresent++
	}
	res.Status, res.Statement, res.New = Accepted, st, len(fresh)
	if err := in.Store.SaveAccepted(ctx, u, res, fresh); err != nil {
		if errors.Is(err, ErrRaced) {
			return Result{Status: Duplicate, FileHash: res.FileHash, Detail: "this exact file was already uploaded"}, nil
		}
		// New movements in a closed period: the close froze it, so nothing
		// of the file enters. Movements already stored do not count: a
		// file that only repeats them is accepted as before.
		var pc *PeriodClosedError
		if errors.As(err, &pc) {
			res.Statement, res.New, res.AlreadyPresent = nil, 0, 0
			res.ClosedPeriod = &pc.Period
			return quarantine(ReasonPeriodClosed, fmt.Sprintf(
				"the file brings new movements dated in the period from %s to %s, which is closed",
				day(pc.Period.Start), day(pc.Period.End.AddDate(0, 0, -1))))
		}
		return res, err
	}
	return res, nil
}

// identityChecks: is this file a statement of this account at all.
func identityChecks(u Upload, st *Statement) (string, string) {
	if u.Bank != "" && st.Bank != u.Bank {
		return ReasonBankMismatch, fmt.Sprintf("file is a %s export, account is %s", st.Bank, u.Bank)
	}
	if u.AccountNumber != "" && st.AccountNumber != "" && !sameAccount(u.AccountNumber, st.AccountNumber) {
		return ReasonAccountMismatch, "the file states another account number"
	}
	return "", ""
}

// checks: does the statement's content hold.
func (in *Ingester) checks(ctx context.Context, u Upload, st *Statement) (string, string, error) {
	if len(st.Movements) == 0 && !(st.OpeningStated && st.ClosingStated) {
		return ReasonEmpty, "no movements and no stated balances", nil
	}
	if err := CheckBalance(st); err != nil {
		return ReasonBalance, err.Error(), nil
	}
	prev, err := in.Store.LastStatement(ctx, u.TenantID, u.AccountID)
	if err != nil {
		return "", "", err
	}
	// The chain is checked only when the new file starts after the previous
	// one ends. Overlapping exports are allowed: deduplication drops the
	// movements that were already stored.
	if prev != nil && !st.From.IsZero() && st.From.After(prev.To) && st.Opening != prev.Closing {
		return ReasonChainGap, chainGapDetail(prev.To, prev.Closing, st.From, st.Opening), nil
	}
	return "", "", nil
}

// chainGapDetail says which days the missing movements are in and by how
// much. The dates are those of the last stored movement and of the first
// movement of the new file: the missing ones are after the first and before
// the second, boundary days included, since an export can be cut mid-day.
func chainGapDetail(prevTo time.Time, prevClosing money.Amount, from time.Time, opening money.Amount) string {
	return fmt.Sprintf("movements are missing between %s and %s: the statement up to %s closed at %s and this file, "+
		"from %s, opens at %s (difference %s). Upload the export that covers those days first",
		day(prevTo), day(from), day(prevTo), prevClosing, day(from), opening, opening-prevClosing)
}

func sameAccount(a, b string) bool {
	clean := func(s string) string {
		var out strings.Builder
		for _, r := range s {
			if r >= '0' && r <= '9' {
				out.WriteRune(r)
			}
		}
		return out.String()
	}
	ca, cb := clean(a), clean(b)
	return ca != "" && (strings.HasSuffix(ca, cb) || strings.HasSuffix(cb, ca))
}

// Parse detects the layout of a file and parses it. On failure it returns a
// reason code and a detail; for an unknown layout the detail is the
// fingerprint of the header it found.
func Parse(data []byte) (*Statement, string, string) {
	p := parse(data)
	if p.reason == ReasonUnknownLayout {
		return nil, p.reason, p.fingerprint
	}
	return p.st, p.reason, p.detail
}

// parsed is the outcome of parse: a statement, or a reason with a detail
// for the tenant and, for an unknown layout, the fingerprint found.
type parsed struct {
	st                          *Statement
	reason, detail, fingerprint string
}

func unknownLayout(sheetNo int, fp string) parsed {
	return parsed{reason: ReasonUnknownLayout, fingerprint: fp,
		detail: prefixSheet(sheetNo, "header fingerprint "+fp+" is not a known layout")}
}

func parse(data []byte) parsed {
	switch {
	case isLegacyXLS(data):
		return parsed{reason: ReasonUnsupportedFormat,
			detail: "Excel 97-2003 files (.xls) are not read: save it as .xlsx or CSV and upload it again"}
	case isWorkbook(data):
		return parseWorkbook(data)
	}
	det := detect(decodeText(data))
	// A line the CSV reader cannot split stops the file with that line,
	// whether or not the header was recognized: the layout is not the
	// problem, the row is (F16). Its contents never go in the detail.
	if det.Broken != nil && (det.Layout != nil || det.Fingerprint == "") {
		return parsed{reason: ReasonRowParse, detail: fmt.Sprintf("line %d: %v", det.Broken.Line, det.Broken.Err)}
	}
	if det.Layout == nil {
		return unknownLayout(0, det.Fingerprint)
	}
	return parseSheet(det.Sheet, det.Layout, det.Fingerprint, 0)
}

// parseSheet runs a layout's parser on a sheet whose fingerprint matched.
// sheetNo is the workbook sheet, 0 for a text file.
func parseSheet(s *sheet, l *Layout, fp string, sheetNo int) parsed {
	st, err := l.parse(s, l)
	if err != nil {
		if errors.Is(err, ErrIncomplete) {
			return parsed{reason: ReasonIncomplete, detail: prefixSheet(sheetNo, err.Error())}
		}
		var re *RowError
		if errors.As(err, &re) {
			re.Sheet = sheetNo
		}
		return parsed{reason: ReasonRowParse, detail: err.Error()}
	}
	st.LayoutID, st.Bank, st.Fingerprint = l.ID, l.Bank, fp
	for i := range st.Movements {
		st.Movements[i].Sheet = sheetNo
	}
	return parsed{st: st}
}

func prefixSheet(n int, s string) string {
	if n > 0 {
		return fmt.Sprintf("sheet %d: %s", n, s)
	}
	return s
}

// movementKeys builds the deduplication key of each movement: account, date,
// amount, running balance when present, references, and the occurrence
// number among identical movements of the same file. The description is
// not part of it (F3): a bank that rewrites a concept between two exports,
// or an edited cell, must not turn one movement into two. Ingest compares
// the description against the stored one and quarantines on a difference.
// Two genuine identical transfers on the same day get different occurrence
// numbers; the same movement in two overlapping exports gets the same key.
func movementKeys(accountID string, mv []Movement) []string {
	keys := make([]string, len(mv))
	count := map[string]int{}
	for i, m := range mv {
		bal := "-"
		if m.Balance != nil {
			bal = m.Balance.String()
		}
		base := strings.Join([]string{accountID, m.Date.Format("2006-01-02"), m.Amount.String(), bal,
			m.Reference, m.ExternalRef}, "\x1f")
		count[base]++
		h := sha256.Sum256([]byte(fmt.Sprintf("%s\x1f%d", base, count[base])))
		keys[i] = hex.EncodeToString(h[:16])
	}
	return keys
}
