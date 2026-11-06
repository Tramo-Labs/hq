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
)

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
	Statement      *Statement // set when accepted
	New            int        // movements stored by this upload
	AlreadyPresent int        // movements skipped because an earlier upload had them
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
	st, reason, detail := Parse(u.Data)
	if st != nil {
		res.Fingerprint, res.LayoutID = st.Fingerprint, st.LayoutID
	} else if reason == ReasonUnknownLayout {
		res.Fingerprint = detail
		detail = "header fingerprint " + detail + " is not a known layout"
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
				"line %d has the date, amount, balance and references of a movement already stored, with another description",
				st.Movements[i].Line))
		}
		res.AlreadyPresent++
	}
	res.Status, res.Statement, res.New = Accepted, st, len(fresh)
	if err := in.Store.SaveAccepted(ctx, u, res, fresh); err != nil {
		if errors.Is(err, ErrRaced) {
			return Result{Status: Duplicate, FileHash: res.FileHash, Detail: "this exact file was already uploaded"}, nil
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
		return ReasonChainGap, fmt.Sprintf("opening differs from the closing of the statement ending %s by %s",
			prev.To.Format("2006-01-02"), st.Opening-prev.Closing), nil
	}
	return "", "", nil
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
	det := detect(decodeText(data))
	// A line the CSV reader cannot split stops the file with that line,
	// whether or not the header was recognized: the layout is not the
	// problem, the row is (F16). Its contents never go in the detail.
	if det.Broken != nil && (det.Layout != nil || det.Fingerprint == "") {
		return nil, ReasonRowParse, fmt.Sprintf("line %d: %v", det.Broken.Line, det.Broken.Err)
	}
	if det.Layout == nil {
		return nil, ReasonUnknownLayout, det.Fingerprint
	}
	st, err := det.Layout.parse(det.Sheet, det.Layout)
	if err != nil {
		if errors.Is(err, ErrIncomplete) {
			return nil, ReasonIncomplete, err.Error()
		}
		return nil, ReasonRowParse, err.Error()
	}
	st.LayoutID, st.Bank, st.Fingerprint = det.Layout.ID, det.Layout.Bank, det.Fingerprint
	return st, "", ""
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
