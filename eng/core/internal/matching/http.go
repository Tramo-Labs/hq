package matching

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
	"github.com/Tramo-Labs/hq/eng/core/internal/listrange"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Handlers implements the matching operations of the generated strict
// server.
type Handlers struct {
	store *PGStore
	cfg   Config
}

// NewHandlers wires the handlers to the store with the default limits.
func NewHandlers(s *PGStore) *Handlers { return &Handlers{store: s, cfg: DefaultConfig} }

var errForbidden = &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden, Detail: "your role cannot do this"}

const listLimit = 500

func (h *Handlers) RunMatching(ctx context.Context, req apigen.RunMatchingRequestObject) (apigen.RunMatchingResponseObject, error) {
	p, _ := identity.PrincipalFrom(ctx)
	if p.Role != identity.RoleOwner && p.Role != identity.RoleOperator {
		return nil, errForbidden
	}
	if _, _, err := monthRange(req.Body.Period); err != nil {
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "period must be YYYY-MM"}
	}
	sum, err := h.store.RunMonth(ctx, req.Body.Period, h.cfg)
	if err != nil {
		return nil, err
	}
	return apigen.RunMatching201JSONResponse{RunId: sum.RunID, Period: sum.Period, Matches: sum.Matches, New: sum.New,
		Pending: sum.Pending, Exceptions: sum.Exceptions}, nil
}

func (h *Handlers) ListMatches(ctx context.Context, req apigen.ListMatchesRequestObject) (apigen.ListMatchesResponseObject, error) {
	r, err := dateFilter(req.Params.Period, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	ms, err := h.store.UndecidedInRange(ctx, Tray(req.Params.Tray), r, listLimit)
	if err != nil {
		return nil, decisionProblem(err)
	}
	out := apigen.ListMatches200JSONResponse{Items: []apigen.Match{}}
	for _, m := range ms {
		am, err := toAPIMatch(m)
		if err != nil {
			return nil, err
		}
		if req.Params.Tray == "exception" {
			am.Tray = apigen.MatchTray("exception") // escalated: it waits in Exceptions now
		}
		out.Items = append(out.Items, am)
	}
	return out, nil
}

func (h *Handlers) ListPending(ctx context.Context, req apigen.ListPendingRequestObject) (apigen.ListPendingResponseObject, error) {
	r, err := dateFilter(req.Params.Period, req.Params.From, req.Params.To)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.LatestPendingInRange(ctx, Tray(req.Params.Tray), r, listLimit)
	if err != nil {
		return nil, decisionProblem(err)
	}
	out := apigen.ListPending200JSONResponse{Items: []apigen.PendingMovement{}}
	for _, r := range rows {
		mid, err := uuid.Parse(r.MovementID)
		if err != nil {
			return nil, err
		}
		pm := apigen.PendingMovement{MovementId: mid, RunId: r.RunID, Tray: apigen.PendingMovementTray(r.Tray),
			Reason: apigen.PendingMovementReason(r.Reason), Candidates: []apigen.Candidate{},
			Date: openapi_types.Date{Time: r.Date}, Amount: int64(r.Amount), Description: r.Description,
			AccountId: r.AccountID, AccountName: r.AccountName, Bank: apigen.Bank(r.Bank)}
		if r.Note != "" {
			note := r.Note
			pm.Note = &note
		}
		if r.MatchID != "" {
			m := r.MatchID
			pm.MatchId = &m
		}
		for _, c := range r.Candidates {
			ids, err := uuids(c.ItemIDs)
			if err != nil {
				return nil, err
			}
			pm.Candidates = append(pm.Candidates, apigen.Candidate{Rule: c.Rule.ID, ItemIds: ids})
		}
		out.Items = append(out.Items, pm)
	}
	return out, nil
}

// ListOpenItems lists what "link by hand" offers: the items no match in
// effect settles. Every member of the tenant sees them.
func (h *Handlers) ListOpenItems(ctx context.Context, req apigen.ListOpenItemsRequestObject) (apigen.ListOpenItemsResponseObject, error) {
	dir := ""
	if req.Params.Direction != nil {
		dir = string(*req.Params.Direction)
	}
	items, more, err := h.store.OpenItems(ctx, periodOf(req.Params.Period), dir, listLimit)
	if err != nil {
		return nil, err
	}
	out := apigen.ListOpenItems200JSONResponse{Items: []apigen.OpenItem{}, Truncated: more}
	for _, it := range items {
		oi := apigen.OpenItem{Id: it.ID, Source: it.Source, SourceId: it.SourceID, Kind: it.Kind,
			Date: openapi_types.Date{Time: it.Date}, Amount: int64(it.Amount), Refs: it.Refs,
			Confidence: float32(it.Confidence), InvoiceId: it.InvoiceID}
		if oi.Refs == nil {
			oi.Refs = []string{}
		}
		if it.Due != nil {
			oi.Due = &openapi_types.Date{Time: *it.Due}
		}
		if it.Party != "" {
			p := it.Party
			oi.Party = &p
		}
		out.Items = append(out.Items, oi)
	}
	return out, nil
}

func (h *Handlers) DecideMatch(ctx context.Context, req apigen.DecideMatchRequestObject) (apigen.DecideMatchResponseObject, error) {
	m, err := h.store.GetMatch(ctx, req.MatchId)
	if errors.Is(err, ErrNotFound) {
		return nil, &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "match not found"}
	}
	if err != nil {
		return nil, err
	}
	in := DecisionInput{Key: req.Params.IdempotencyKey.String(), Match: m, Action: Action(req.Body.Action)}
	if req.Body.Note != nil {
		in.Note = *req.Body.Note
	}
	got, _, err := h.store.Record(ctx, in)
	if err != nil {
		return nil, decisionProblem(err)
	}
	uid, err := uuid.Parse(got.By)
	if err != nil {
		return nil, err
	}
	out := apigen.DecideMatch201JSONResponse{MatchId: got.MatchID, Tray: apigen.DecisionTray(got.Tray),
		Action: apigen.DecisionAction(got.Action), UserId: uid, Role: apigen.Role(got.Role), At: got.At,
		Revision: got.Revision}
	if got.Note != "" {
		note := got.Note
		out.Note = &note
	}
	return out, nil
}

func (h *Handlers) DecideMovement(ctx context.Context, req apigen.DecideMovementRequestObject) (apigen.DecideMovementResponseObject, error) {
	in := MovementDecisionInput{Key: req.Params.IdempotencyKey.String(), MovementID: req.MovementId.String(),
		Action: MovementAction(req.Body.Action)}
	if req.Body.Note != nil {
		in.Note = *req.Body.Note
	}
	if req.Body.ItemIds != nil {
		for _, id := range *req.Body.ItemIds {
			in.ItemIDs = append(in.ItemIDs, id.String())
		}
	}
	got, _, err := h.store.DecideMovement(ctx, in)
	if err != nil {
		if errors.Is(err, ErrUnknownMovement) {
			return nil, &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "movement not found"}
		}
		return nil, decisionProblem(err)
	}
	mid, err := uuid.Parse(got.MovementID)
	if err != nil {
		return nil, err
	}
	uid, err := uuid.Parse(got.By)
	if err != nil {
		return nil, err
	}
	out := apigen.DecideMovement201JSONResponse{MovementId: mid, Action: apigen.MovementDecisionAction(got.Action),
		UserId: uid, Role: apigen.Role(got.Role), At: got.At, Revision: got.Revision}
	if got.Note != "" {
		note := got.Note
		out.Note = &note
	}
	if got.MatchID != "" {
		m := got.MatchID
		out.MatchId = &m
	}
	return out, nil
}

// decisionProblem maps the store's decision errors to problems. Every
// detail is a fixed sentence: never the error's text, which can carry ids,
// input or internals. Anything not listed stays an error (500, logged).
func decisionProblem(err error) error {
	conflict := func(code, detail string) error {
		return &httpx.Error{Status: http.StatusConflict, Code: code, Detail: detail}
	}
	bad := func(code, detail string) error {
		return &httpx.Error{Status: http.StatusBadRequest, Code: code, Detail: detail}
	}
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNoUser):
		return errForbidden
	case errors.Is(err, ErrAlreadyDecided):
		return conflict(httpx.CodeConflict, "there is already a decision in effect")
	case errors.Is(err, ErrNothingToRevert):
		return conflict("nothing_to_revert", "there is no decision in effect to revert")
	case errors.Is(err, ErrClosed):
		return conflict("period_closed", "the period is closed")
	case errors.Is(err, ErrNotInExceptions):
		return conflict("not_in_exceptions", "the movement is not in the Exceptions tray without a candidate")
	case errors.Is(err, ErrOperatorAnswered):
		return conflict("operator_answered", "the operator already answered this reject")
	case errors.Is(err, ErrRevertOnMatch):
		return conflict("revert_on_match", "revert the reject on the match")
	case errors.Is(err, ErrNotWithOperator):
		return conflict("not_with_operator", "the movement is not waiting for the operator")
	case errors.Is(err, ErrItemTaken):
		return conflict("item_taken", "an item is already settled by another match")
	case errors.Is(err, ErrNoteRequired):
		return bad("note_required", "note required: say why")
	case errors.Is(err, ErrActionNotInTray):
		return bad(httpx.CodeBadRequest, "this action is not available in the item's tray")
	case errors.Is(err, ErrLinkDoesNotAdd):
		return bad(httpx.CodeBadRequest, "the items do not add up exactly to the movement")
	case errors.Is(err, ErrUnknownItem):
		return bad(httpx.CodeBadRequest, "an item to link does not exist")
	case errors.Is(err, ErrInvalid):
		return bad(httpx.CodeBadRequest, "invalid decision")
	case errors.Is(err, ErrKeyReused):
		return &httpx.Error{Status: http.StatusUnprocessableEntity, Code: httpx.CodeIdempotencyMismatch, Detail: "key used for another decision"}
	}
	return err
}

func toAPIMatch(m Match) (apigen.Match, error) {
	mv, err := uuids(m.MovementIDs)
	if err != nil {
		return apigen.Match{}, err
	}
	it, err := uuids(m.ItemIDs)
	if err != nil {
		return apigen.Match{}, err
	}
	out := apigen.Match{Id: m.ID, Rule: m.Rule.ID, RuleVersion: m.Rule.Version, Tray: apigen.MatchTray(m.Tray),
		MovementIds: mv, ItemIds: it, Adjustments: []apigen.Adjustment{}, CrossPeriod: m.CrossPeriod,
		LowConfidence: m.LowConfidence}
	for _, a := range m.Adjustments {
		out.Adjustments = append(out.Adjustments, apigen.Adjustment{Kind: a.Kind, Amount: int64(a.Amount),
			Source: apigen.AdjustmentSource(a.Source)})
	}
	if m.Reference != "" {
		ref := m.Reference
		out.Reference = &ref
	}
	if m.DateDiffDays != 0 {
		d := m.DateDiffDays
		out.DateDiffDays = &d
	}
	return out, nil
}

// dateFilter reads a list's period or from and to; the rules the spec
// cannot express (they go together, not with period, to not before from)
// are 400.
func dateFilter(period *apigen.PeriodFilter, from *apigen.RangeFrom, to *apigen.RangeTo) (*listrange.Range, error) {
	var f, t *time.Time
	if from != nil {
		f = &from.Time
	}
	if to != nil {
		t = &to.Time
	}
	r, err := listrange.Parse(period, f, t)
	if err != nil {
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: err.Error()}
	}
	return r, nil
}

// periodOf reads an optional period filter ("" when absent).
func periodOf(p *apigen.PeriodFilter) string {
	if p == nil {
		return ""
	}
	return *p
}
