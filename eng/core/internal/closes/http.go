package closes

import (
	"context"
	"errors"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Handlers implements the closes operations of the generated strict server
// interface.
type Handlers struct{ svc *Service }

// NewHandlers returns the closes handlers over the service.
func NewHandlers(s *Service) *Handlers { return &Handlers{svc: s} }

// ListCloses lists the closed periods of the active tenant, newest first.
func (h *Handlers) ListCloses(ctx context.Context, req apigen.ListClosesRequestObject) (apigen.ListClosesResponseObject, error) {
	var before *time.Time
	if req.Params.Cursor != nil {
		d, err := time.Parse(time.DateOnly, *req.Params.Cursor)
		if err != nil {
			return nil, &httpx.Error{Status: http.StatusBadRequest, Code: httpx.CodeBadRequest, Detail: "cursor is not one this API gave"}
		}
		before = &d
	}
	cs, more, err := h.svc.List(ctx, before)
	if err != nil {
		return nil, err
	}
	out := apigen.ListCloses200JSONResponse{Items: make([]apigen.Close, 0, len(cs))}
	for _, c := range cs {
		out.Items = append(out.Items, toAPI(c))
	}
	if more && len(cs) > 0 {
		next := cs[len(cs)-1].Period.Start.Format(time.DateOnly)
		out.NextCursor = &next
	}
	return out, nil
}

// ClosePeriod closes a period of the active tenant (operator only).
func (h *Handlers) ClosePeriod(ctx context.Context, req apigen.ClosePeriodRequestObject) (apigen.ClosePeriodResponseObject, error) {
	c, err := h.svc.ClosePeriod(ctx, req.Body.PeriodStart.Time)
	var pending *PendingError
	switch {
	case err == nil:
		return apigen.ClosePeriod201JSONResponse(toAPI(c)), nil
	case errors.As(err, &pending):
		n := pending.N
		detail := "the period still has Suggested items; decide them first"
		return apigen.ClosePeriod409ApplicationProblemPlusJSONResponse{Type: "about:blank",
			Title: http.StatusText(http.StatusConflict), Status: http.StatusConflict, Code: "suggested_pending",
			Detail: &detail, PendingSuggested: &n, RequestId: requestID(ctx)}, nil
	case errors.Is(err, ErrForbidden):
		return nil, &httpx.Error{Status: http.StatusForbidden, Code: httpx.CodeForbidden, Detail: "only an operator closes a period"}
	case errors.Is(err, ErrInvalidPeriod):
		return nil, &httpx.Error{Status: http.StatusBadRequest, Code: "invalid_period", Detail: "periodStart is not the first day of a period"}
	case errors.Is(err, ErrNotOver):
		return nil, &httpx.Error{Status: http.StatusConflict, Code: "period_not_over", Detail: "the period has not ended yet"}
	case errors.Is(err, ErrAlreadyClosed):
		return nil, &httpx.Error{Status: http.StatusConflict, Code: "already_closed", Detail: "that period is already closed"}
	case errors.Is(err, ErrNoNumbers):
		return nil, &httpx.Error{Status: http.StatusNotImplemented, Code: "not_implemented", Detail: "the period's numbers are not available yet"}
	}
	return nil, err
}

// GetClose returns one closed period of the active tenant.
func (h *Handlers) GetClose(ctx context.Context, req apigen.GetCloseRequestObject) (apigen.GetCloseResponseObject, error) {
	c, err := h.svc.Get(ctx, req.CloseId)
	if errors.Is(err, ErrNotFound) {
		return nil, &httpx.Error{Status: http.StatusNotFound, Code: httpx.CodeNotFound, Detail: "no such close"}
	}
	if err != nil {
		return nil, err
	}
	return apigen.GetClose200JSONResponse(toAPI(c)), nil
}

func requestID(ctx context.Context) *string {
	if m, ok := httpx.MetaFrom(ctx); ok && m.RequestID != "" {
		id := m.RequestID
		return &id
	}
	return nil
}

// RangeLabel is how a period is named in a metric: first and last day,
// both included.
func RangeLabel(p Period) string {
	return p.Start.Format(time.DateOnly) + "/" + p.LastDay().Format(time.DateOnly)
}

// ToAPIRange is a period as the API shows it: first and last day included.
func ToAPIRange(p Period) apigen.PeriodRange {
	return apigen.PeriodRange{From: openapi_types.Date{Time: p.Start}, To: openapi_types.Date{Time: p.LastDay()}}
}

func toAPI(c Close) apigen.Close {
	out := apigen.Close{Id: c.ID, Cadence: apigen.CloseCadence(c.Period.Cadence),
		PeriodStart: openapi_types.Date{Time: c.Period.Start}, PeriodEnd: openapi_types.Date{Time: c.Period.LastDay()},
		ClosedAt: c.ClosedAt, ClosedBy: c.ClosedBy, Summary: make([]apigen.Metric, 0, len(c.Summary))}
	label := RangeLabel(c.Period)
	for _, m := range c.Summary {
		st := apigen.MetricMeasured
		if m.Value == nil {
			st = apigen.MetricNoData
		}
		out.Summary = append(out.Summary, apigen.Metric{Key: m.Key, Label: m.Label, Status: st, Value: m.Value,
			Unit: apigen.MetricUnit(m.Unit), Period: label, Source: m.Source, List: m.List})
	}
	return out
}
