package matching

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/identity"
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
	ms, err := h.store.UndecidedIn(ctx, Tray(req.Params.Tray), periodOf(req.Params.Period), listLimit)
	if err != nil {
		return nil, err
	}
	out := apigen.ListMatches200JSONResponse{Items: []apigen.Match{}}
	for _, m := range ms {
		am, err := toAPIMatch(m)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, am)
	}
	return out, nil
}

func (h *Handlers) ListPending(ctx context.Context, req apigen.ListPendingRequestObject) (apigen.ListPendingResponseObject, error) {
	rows, err := h.store.LatestPendingIn(ctx, Tray(req.Params.Tray), periodOf(req.Params.Period), listLimit)
	if err != nil {
		return nil, err
	}
	out := apigen.ListPending200JSONResponse{Items: []apigen.PendingMovement{}}
	for _, r := range rows {
		mid, err := uuid.Parse(r.MovementID)
		if err != nil {
			return nil, err
		}
		pm := apigen.PendingMovement{MovementId: mid, RunId: r.RunID, Tray: apigen.PendingMovementTray(r.Tray),
			Reason: apigen.PendingMovementReason(r.Reason), Candidates: []apigen.Candidate{}}
		if r.Note != "" {
			note := r.Note
			pm.Note = &note
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
		if errors.Is(err, ErrInvalid) && strings.Contains(err.Error(), "unknown movement") {
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

// decisionProblem maps the store's decision errors to problems.
func decisionProblem(err error) error {
	conflict := func(code, detail string) error {
		return &httpx.Error{Status: http.StatusConflict, Code: code, Detail: detail}
	}
	switch {
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrNoUser):
		return errForbidden
	case errors.Is(err, ErrAlreadyDecided):
		return conflict(httpx.CodeConflict, "there is already a decision in effect")
	case errors.Is(err, ErrNothingToRevert):
		return conflict("nothing_to_revert", ErrNothingToRevert.Error())
	case errors.Is(err, ErrClosed):
		return conflict("period_closed", ErrClosed.Error())
	case errors.Is(err, ErrNotInExceptions):
		return conflict("not_in_exceptions", ErrNotInExceptions.Error())
	case errors.Is(err, ErrItemTaken):
		return conflict("item_taken", ErrItemTaken.Error())
	case errors.Is(err, ErrActionNotInTray), errors.Is(err, ErrLinkDoesNotAdd), errors.Is(err, ErrInvalid):
		return &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: err.Error()}
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

// periodOf reads an optional period filter ("" when absent).
func periodOf(p *apigen.PeriodFilter) string {
	if p == nil {
		return ""
	}
	return *p
}
