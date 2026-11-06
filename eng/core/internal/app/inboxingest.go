package app

import (
	"context"

	"github.com/Tramo-Labs/hq/eng/core/internal/inbox"
	"github.com/Tramo-Labs/hq/eng/core/internal/ledger"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// LedgerInbox is ingestion without an account (datos) as the inbox takes
// it: each mailed attachment is routed by bank and number, or quarantined.
// The inbox runs it under the tenant's scope with no user. Deps.InboxIngest
// takes it (cmd/api and cmd/dev wire it).
func LedgerInbox(d *db.DB) inbox.Ingest {
	s := &ledger.PGStore{DB: d}
	return ledgerInbox{in: &ledger.Ingester{Store: s}, router: s}
}

type ledgerInbox struct {
	in     *ledger.Ingester
	router ledger.Router
}

func (l ledgerInbox) IngestUnassigned(ctx context.Context, a inbox.Unassigned) (ledger.Result, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return ledger.Result{}, db.ErrNoScope
	}
	return l.in.IngestUnassigned(ctx, l.router, ledger.Unassigned{TenantID: sc.TenantID.String(),
		Filename: a.Filename, Data: a.Data, ReceivedAt: a.ReceivedAt})
}
