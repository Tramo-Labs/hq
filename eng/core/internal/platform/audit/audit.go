// Package audit writes the append only audit log (eng/ARQUITECTURA.md
// section 6): who did what, on which tenant, from which IP and with which
// request id. Rows can not be updated or deleted (trigger in migration
// 00001), and tenant transactions can only read and write their own rows.
//
// Record takes the transaction of the action it audits, so the action and
// its audit row commit or roll back together. Detail must not carry
// passwords, tokens, keys, CUIT of third parties or amounts.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Entry is one audited action.
type Entry struct {
	// Action is a dotted verb, e.g. "identity.login", "matching.approve".
	Action string
	// Subject is "<table>:<id>" of what was acted upon.
	Subject string
	// TenantID and UserID override the scope in the context (login happens
	// before a scope exists). Zero values mean "take them from the context".
	TenantID uuid.UUID
	UserID   uuid.UUID
	Detail   map[string]any
}

// Record inserts e in tx, filling tenant, user, request id and IP from ctx.
func Record(ctx context.Context, tx pgx.Tx, e Entry) error {
	if e.Action == "" || e.Subject == "" {
		return fmt.Errorf("audit: action and subject are required")
	}
	if s, ok := db.ScopeFrom(ctx); ok {
		if e.TenantID == uuid.Nil {
			e.TenantID = s.TenantID
		}
		if e.UserID == uuid.Nil {
			e.UserID = s.UserID
		}
	}
	var requestID, ip any
	if m, ok := httpx.MetaFrom(ctx); ok {
		requestID = m.RequestID
		if m.IP.IsValid() {
			ip = m.IP.String()
		}
	}
	var detail any
	if len(e.Detail) > 0 {
		b, err := json.Marshal(e.Detail)
		if err != nil {
			return fmt.Errorf("audit: detail: %w", err)
		}
		detail = b
	}
	_, err := tx.Exec(ctx,
		`insert into audit_log (tenant_id, user_id, action, subject, request_id, ip, detail)
		 values ($1, $2, $3, $4, $5, $6::inet, $7)`,
		nullUUID(e.TenantID), nullUUID(e.UserID), e.Action, e.Subject, requestID, ip, detail)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	return nil
}

func nullUUID(u uuid.UUID) any {
	if u == uuid.Nil {
		return nil
	}
	return u
}
