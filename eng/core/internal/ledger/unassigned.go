package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/ledger/store"
)

// Unassigned is a statement that arrived with no account (the per tenant
// inbox): ingestion finds the account, or quarantines the file.
type Unassigned struct {
	TenantID   string
	Filename   string
	Data       []byte
	ReceivedAt time.Time
}

// AccountRef is what routing needs of one of the tenant's accounts.
type AccountRef struct {
	ID           string
	Number       string
	Currency     string
	HasStatement bool // it has an accepted statement already
}

// Router lists the tenant's accounts of a bank.
type Router interface {
	AccountsOfBank(ctx context.Context, tenantID string, bank Bank) ([]AccountRef, error)
}

// ReasonUnrouted: a readable file that arrived without an account and that
// no single account of the tenant matches. A file that cannot be read keeps
// its reading reason (unknown_layout, unsupported_format, row_parse).
const ReasonUnrouted = "unrouted"

// IngestUnassigned routes a file that arrived with no account, then runs
// it through Ingest like an upload. The rule (lead, 06/11, by default):
//
//  1. the file states its account number: the account of that bank with
//     that number; none, or more than one, is unrouted;
//  2. it does not: the tenant's only account of that bank (and currency,
//     when the layout states it), and only when that account already has
//     an accepted statement: the first statement of an account is
//     uploaded by a person, so a second account nobody registered never
//     lands on it unnoticed;
//  3. anything else is unrouted, never a guess.
//
// After routing every check of an upload runs: balances, chain, duplicates,
// closed periods. How the file was routed is stored with it.
func (in *Ingester) IngestUnassigned(ctx context.Context, r Router, a Unassigned) (Result, error) {
	u := Upload{TenantID: a.TenantID, Filename: a.Filename, Data: a.Data, ReceivedAt: a.ReceivedAt, Source: SourceMail}
	sum := sha256.Sum256(a.Data)
	res := Result{FileHash: hex.EncodeToString(sum[:])}

	p := parse(a.Data)
	if p.st == nil {
		// Not readable: there is no bank to route by. It keeps its reading
		// reason. A file that reads but fails a later check is routed
		// first, so that reason lands on its account.
		res.Fingerprint = p.fingerprint
		return in.quarantineUnassigned(ctx, u, res, p.reason, p.detail)
	}
	st := p.st
	res.Fingerprint, res.LayoutID = st.Fingerprint, st.LayoutID

	accounts, err := r.AccountsOfBank(ctx, a.TenantID, st.Bank)
	if err != nil {
		return res, err
	}
	acct, how, why := route(st, accounts)
	if acct == nil {
		return in.quarantineUnassigned(ctx, u, res, ReasonUnrouted, why)
	}
	u.AccountID, u.Bank, u.AccountNumber, u.Routed = acct.ID, st.Bank, acct.Number, how
	out, err := in.Ingest(ctx, u)
	out.AccountID, out.Routed = acct.ID, how
	return out, err
}

// quarantineUnassigned keeps a file with no account; a file already
// accepted (uploaded by hand, or mailed twice) is a duplicate instead.
func (in *Ingester) quarantineUnassigned(ctx context.Context, u Upload, res Result, reason, detail string) (Result, error) {
	seen, err := in.Store.FileSeen(ctx, u.TenantID, res.FileHash)
	if err != nil {
		return res, err
	}
	if seen {
		return Result{Status: Duplicate, FileHash: res.FileHash, Fingerprint: res.Fingerprint, LayoutID: res.LayoutID,
			Detail: "this exact file was already uploaded"}, nil
	}
	res.Status, res.Reason, res.Detail = Quarantined, reason, detail
	return res, in.Store.SaveQuarantined(ctx, u, res)
}

// route applies the rule of IngestUnassigned: the account, how it was
// found, or why none was. The reasons name the bank, never a number.
func route(st *Statement, accounts []AccountRef) (*AccountRef, string, string) {
	if st.CurrencyStated {
		var same []AccountRef
		for _, a := range accounts {
			if a.Currency == st.Currency {
				same = append(same, a)
			}
		}
		accounts = same
	}
	if st.AccountNumber != "" {
		var hit []AccountRef
		for _, a := range accounts {
			if a.Number != "" && sameAccount(a.Number, st.AccountNumber) {
				hit = append(hit, a)
			}
		}
		switch len(hit) {
		case 1:
			return &hit[0], RoutedByNumber, ""
		case 0:
			return nil, "", fmt.Sprintf("no %s account of the tenant has the number the file states", st.Bank)
		}
		return nil, "", fmt.Sprintf("%d %s accounts of the tenant have the number the file states", len(hit), st.Bank)
	}
	switch {
	case len(accounts) == 0:
		return nil, "", fmt.Sprintf("the tenant has no %s account", st.Bank)
	case len(accounts) > 1:
		return nil, "", fmt.Sprintf("the file does not state its account number and the tenant has %d %s accounts",
			len(accounts), st.Bank)
	case !accounts[0].HasStatement:
		return nil, "", fmt.Sprintf("the file does not state its account number, and the only %s account has no statement yet: "+
			"its first statement is uploaded by hand", st.Bank)
	}
	return &accounts[0], RoutedSingleAccount, ""
}

// AccountsOfBank lists the tenant's accounts of a bank (Router).
func (s *PGStore) AccountsOfBank(ctx context.Context, tenantID string, bank Bank) ([]AccountRef, error) {
	var out []AccountRef
	err := s.inTenant(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := store.New(tx).AccountsOfBank(ctx, string(bank))
		for _, r := range rows {
			out = append(out, AccountRef{ID: r.ID.String(), Number: r.Number, Currency: r.Currency, HasStatement: r.HasStatement})
		}
		return err
	})
	return out, err
}
