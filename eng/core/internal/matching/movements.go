package matching

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/money"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/audit"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
)

// RuleManual is the rule of a link the client made by hand.
var RuleManual = Rule{"manual_link", 1}

// Manual is the tray of a match the client linked by hand.
const Manual Tray = "manual"

// MovementAction is what the client can do with a movement that has no
// candidate (product spec section 4, "Sin propuesta: contanos qué es").
type MovementAction string

const (
	// MovementOther: it is something else; note required, back to the operator.
	MovementOther MovementAction = "other"
	// MovementLink: linked by hand to open items that add up exactly.
	MovementLink MovementAction = "link"
	// MovementRevert undoes the decision in effect, as a new recorded row.
	MovementRevert MovementAction = "revert"
	// MovementResolve: the operator closes a movement the client marked
	// other, saying what was done with it (note required).
	MovementResolve MovementAction = "resolve"
	// MovementReturn: the operator sends it back to the client with what
	// the operator needs (note required). It shows again in Exceptions.
	MovementReturn MovementAction = "return"
	// MovementRejected is written by a client's reject of a match in
	// Exceptions, one row per movement of the match, never by this
	// endpoint: the movement is with the operator like after "other", and
	// the client reverts it on the match.
	MovementRejected MovementAction = "rejected"
)

var (
	// ErrNotInExceptions: the movement is not waiting in the Exceptions tray
	// without a candidate in the latest run of its month.
	ErrNotInExceptions = errors.New("matching: the movement is not in the Exceptions tray without a candidate")
	// ErrLinkDoesNotAdd: the linked items do not add up exactly to the
	// movement. Any difference is an adjustment, and adjustments are the
	// operator's (Suggested).
	ErrLinkDoesNotAdd = errors.New("matching: the items do not add up exactly to the movement")
	// ErrItemTaken: an item is already settled by a match in effect.
	ErrItemTaken = errors.New("matching: an item is already settled by another match")
	// ErrNotWithOperator: the operator acts only on a movement the client
	// marked other, or whose match the client rejected, and nobody
	// answered yet.
	ErrNotWithOperator = errors.New("matching: the movement is not waiting for the operator")
	// ErrRevertOnMatch: a client's reject is reverted on the match it
	// rejected, not on each movement.
	ErrRevertOnMatch = errors.New("matching: revert the reject on the match")
)

// MovementDecisionInput is what the client may say. Who decides and with
// which role are never input.
type MovementDecisionInput struct {
	Key        string // idempotency key
	MovementID string
	Action     MovementAction
	Note       string
	ItemIDs    []string // for MovementLink
}

// MovementDecision is a stored decision on a movement.
type MovementDecision struct {
	MovementID string
	Action     MovementAction
	MatchID    string // the manual match, for MovementLink
	Note       string
	By, Role   string
	Revision   int
	At         time.Time
}

// DecideMovement records a decision on a movement of the Exceptions tray
// that has no candidate, or reverts the one in effect. Role read from
// memberships here. The client (owner or approver) says other, link, or
// reverts its own decision; after the client's other, the operator
// resolves, links or returns it, or reverts its own answer, which puts the
// client's other back in effect. A link creates a manual match (rule
// manual_link@v1) that is settled for the next runs while it is in effect.
func (s *PGStore) DecideMovement(ctx context.Context, in MovementDecisionInput) (MovementDecision, bool, error) {
	sc, ok := db.ScopeFrom(ctx)
	if !ok {
		return MovementDecision{}, false, db.ErrNoScope
	}
	if sc.UserID == uuid.Nil {
		return MovementDecision{}, false, ErrNoUser
	}
	key, err := uuid.Parse(in.Key)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	mid, err := uuid.Parse(in.MovementID)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	switch in.Action {
	case MovementOther, MovementResolve, MovementReturn:
		if noteMissing(in.Note) {
			return MovementDecision{}, false, ErrNoteRequired
		}
	case MovementLink:
		if len(in.ItemIDs) == 0 {
			return MovementDecision{}, false, ErrInvalid
		}
	case MovementRevert:
	default:
		return MovementDecision{}, false, ErrInvalid
	}
	items, err := uuids(in.ItemIDs)
	if err != nil {
		return MovementDecision{}, false, ErrInvalid
	}
	d := MovementDecision{MovementID: mid.String(), Action: in.Action, Note: in.Note, By: sc.UserID.String()}
	applied := false
	err = s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		var prevMove uuid.UUID
		var prevAction, prevRole, prevNote string
		var prevMatch *string
		var prevUser uuid.UUID
		var prevRev int
		var prevAt time.Time
		// The role first (see Record): someone who decides on no movement
		// is 403 whatever key it sends.
		var err error
		if d.Role, err = memberRole(ctx, tx); err != nil {
			return err
		}
		client, operator := CanDecide(d.Role, Exception), d.Role == identity.RoleOperator
		if !client && !operator {
			return ErrForbidden
		}
		err = tx.QueryRow(ctx,
			`select movement_id, action, user_id, role, note, match_id, revision, at from matching.movement_decisions where key = $1`, key).
			Scan(&prevMove, &prevAction, &prevUser, &prevRole, &prevNote, &prevMatch, &prevRev, &prevAt)
		if err == nil {
			if prevMove == mid && prevAction == string(in.Action) && prevUser == sc.UserID {
				d.Role, d.Note, d.Revision, d.At = prevRole, prevNote, prevRev, prevAt
				if prevMatch != nil {
					d.MatchID = *prevMatch
				}
				return nil
			}
			return ErrKeyReused
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		var amount int64
		var date time.Time
		err = tx.QueryRow(ctx, `select amount, date from ledger.movements where id = $1`, mid).Scan(&amount, &date)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: unknown movement", ErrInvalid)
		}
		if err != nil {
			return err
		}
		var cur struct {
			id       int64
			revision int
			action   string
			role     string
		}
		err = tx.QueryRow(ctx,
			`select coalesce(id, 0), revision, coalesce(action, ''), coalesce(role, '')
			   from matching.movement_decision_state where movement_id = $1`, mid).
			Scan(&cur.id, &cur.revision, &cur.action, &cur.role)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := movementTurn(in.Action, client, MovementAction(cur.action), cur.role); err != nil {
			return err
		}

		var reverts *int64
		var matchID *string
		switch in.Action {
		case MovementRevert:
			isClosed, err := s.closed(ctx, tx, []string{period(date)})
			if err != nil {
				return err
			}
			if isClosed {
				return ErrClosed
			}
			reverts = &cur.id
		default:
			// The first decision on a movement needs it waiting in the
			// client's Exceptions without a candidate. After that the turn
			// is the decision in effect (movementTurn): a rejected match's
			// movement, or one the operator returned, need not be pending.
			waiting := cur.action != ""
			if !waiting {
				if err := tx.QueryRow(ctx, `
				with latest as (select distinct on (period) id from matching.runs order by period, started_at desc, id)
				select exists (select 1 from matching.pending
				                where run_id in (select id from latest) and movement_id = $1
				                  and tray = 'exception' and reason = 'no_candidate')`, mid).Scan(&waiting); err != nil {
					return err
				}
			}
			if !waiting {
				return ErrNotInExceptions
			}
			if in.Action == MovementLink {
				id, err := linkByHand(ctx, tx, mid, money.Amount(amount), date, items)
				if err != nil {
					return err
				}
				matchID, d.MatchID = &id, id
			}
		}
		d.Revision = cur.revision + 1
		if s.beforeInsert != nil {
			s.beforeInsert()
		}

		err = tx.QueryRow(ctx,
			`insert into matching.movement_decisions (tenant_id, key, movement_id, revision, action, match_id, note, user_id, role, reverts)
			 values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9) returning at`,
			key, mid, d.Revision, string(d.Action), matchID, d.Note, sc.UserID, d.Role, reverts).Scan(&d.At)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "movement_decisions_tenant_id_movement_id_revision_key" {
			return ErrAlreadyDecided
		}
		if err != nil {
			return err
		}
		if in.Action == MovementRevert && cur.action == string(MovementLink) {
			var linked *string
			_ = tx.QueryRow(ctx, `select match_id from matching.movement_decisions where id = $1`, cur.id).Scan(&linked)
			if linked != nil {
				d.MatchID = *linked // the manual match that stops being in effect
			}
		}
		applied = true
		detail := map[string]any{"role": d.Role, "revision": d.Revision}
		if d.MatchID != "" {
			detail["match"] = d.MatchID
		}
		return audit.Record(ctx, tx, audit.Entry{Action: "matching.movement_" + string(d.Action),
			Subject: "ledger.movements:" + mid.String(), Detail: detail})
	})
	if err != nil {
		return MovementDecision{}, false, err
	}
	return d, applied, nil
}

// movementTurn says whether the caller's side may take the action given the
// decision in effect (action and the role that took it). The client acts
// on a movement nobody decided or that the operator returned; the operator
// acts on one the client marked other. Each side reverts only its own row.
func movementTurn(a MovementAction, client bool, cur MovementAction, curRole string) error {
	curByClient := curRole == identity.RoleOwner || curRole == identity.RoleApprover
	if a == MovementRevert {
		switch {
		case cur == "":
			return ErrNothingToRevert
		case client != curByClient:
			return ErrForbidden
		case cur == MovementRejected:
			return ErrRevertOnMatch
		}
		return nil
	}
	if client {
		switch a {
		case MovementOther, MovementLink:
		default:
			return ErrForbidden
		}
		if cur != "" && cur != MovementReturn {
			return ErrAlreadyDecided
		}
		return nil
	}
	switch a {
	case MovementResolve, MovementReturn, MovementLink:
	default:
		return ErrForbidden
	}
	if cur != MovementOther && cur != MovementRejected {
		return ErrNotWithOperator
	}
	return nil
}

// matchInEffect is the SQL condition, on a match aliased m, of a match
// that settles its items now: automatic, approved in either tray, or a
// manual link in effect. The database guard on items (migration 00104)
// states the same rule.
const matchInEffect = `(m.tray = 'auto'
	or exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id and d.action = 'approve')
	or (m.tray = 'manual' and exists (select 1 from matching.movement_decision_state s
	     where s.tenant_id = m.tenant_id and s.match_id = m.id and s.action = 'link')))`

// OpenItem is an item no match in effect settles.
type OpenItem struct {
	ID         uuid.UUID
	Source     string
	SourceID   string
	Kind       string
	Date       time.Time
	Due        *time.Time
	Amount     money.Amount
	Refs       []string
	Party      string
	Confidence float64
	InvoiceID  *uuid.UUID
}

// OpenItems lists the items no match in effect settles, oldest first and
// then by source id, so the list always comes in the same order: what
// "link by hand" offers. period (YYYY-MM) keeps the items dated in that
// month, "" all; direction "in" keeps positive amounts, "out" negative, ""
// both. Up to limit rows; the bool says there were more.
func (s *PGStore) OpenItems(ctx context.Context, period, direction string, limit int) ([]OpenItem, bool, error) {
	var out []OpenItem
	err := s.DB.InTenant(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			select i.id, i.source, i.source_id, i.kind, i.date, i.due, i.amount, i.refs, i.party, i.confidence, i.invoice_id
			  from matching.items i
			 where not exists (select 1 from matching.matches m where i.id = any(m.item_ids) and `+matchInEffect+`)
			   and ($1 = '' or to_char(i.date, 'YYYY-MM') = $1)
			   and ($2 = '' or ($2 = 'in' and i.amount > 0) or ($2 = 'out' and i.amount < 0))
			 order by i.date, i.source_id, i.id
			 limit $3`, period, direction, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var it OpenItem
			var amount int64
			var conf float32
			if err := rows.Scan(&it.ID, &it.Source, &it.SourceID, &it.Kind, &it.Date, &it.Due, &amount, &it.Refs,
				&it.Party, &conf, &it.InvoiceID); err != nil {
				return err
			}
			it.Amount, it.Confidence = money.Amount(amount), math.Round(float64(conf)*1e4)/1e4 // real column: no float32 noise
			out = append(out, it)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// linkByHand checks the items and creates (or finds) the manual match.
func linkByHand(ctx context.Context, tx pgx.Tx, mid uuid.UUID, amount money.Amount, date time.Time, items []uuid.UUID) (string, error) {
	slices.SortFunc(items, func(a, b uuid.UUID) int { return compareUUID(a, b) })
	items = slices.Compact(items)
	var found int
	var sum int64
	if err := tx.QueryRow(ctx, `select count(*), coalesce(sum(amount), 0) from matching.items where id = any($1)`, items).
		Scan(&found, &sum); err != nil {
		return "", err
	}
	if found != len(items) {
		return "", fmt.Errorf("%w: unknown item", ErrInvalid)
	}
	if money.Amount(sum) != amount {
		return "", ErrLinkDoesNotAdd
	}
	var taken bool
	if err := tx.QueryRow(ctx,
		`select exists (select 1 from matching.matches m where m.item_ids && $1 and `+matchInEffect+`)`, items).Scan(&taken); err != nil {
		return "", err
	}
	if taken {
		return "", ErrItemTaken
	}
	ids := make([]string, len(items))
	for i, u := range items {
		ids[i] = u.String()
	}
	id := matchID(RuleManual, []string{mid.String()}, ids)
	ev, err := json.Marshal(map[string]any{"movement_periods": []string{period(date)}, "linked_by_hand": true})
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx,
		`insert into matching.matches (tenant_id, id, run_id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence)
		 values (app_current_tenant(), $1, null, $2, $3, 'manual', $4, $5, '[]', $6)
		 on conflict (tenant_id, id) do nothing`,
		id, RuleManual.ID, RuleManual.Version, []uuid.UUID{mid}, items, ev)
	return id, err
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
