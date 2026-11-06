package closes

import (
	"context"
	"net/http"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Handlers implements the closes operations of the generated strict server
// interface.
//
// This commit only lands the contract (spec first): every operation
// answers 501 until the closes table and the store come in the next one.
type Handlers struct{}

// NewHandlers returns the closes handlers.
func NewHandlers() *Handlers { return &Handlers{} }

var errNotYet = &httpx.Error{Status: http.StatusNotImplemented, Code: "not_implemented", Detail: "period close not built yet"}

// ListCloses lists the closed periods of the active tenant.
func (h *Handlers) ListCloses(context.Context, apigen.ListClosesRequestObject) (apigen.ListClosesResponseObject, error) {
	return nil, errNotYet
}

// ClosePeriod closes a period of the active tenant.
func (h *Handlers) ClosePeriod(context.Context, apigen.ClosePeriodRequestObject) (apigen.ClosePeriodResponseObject, error) {
	return nil, errNotYet
}

// GetClose returns one closed period.
func (h *Handlers) GetClose(context.Context, apigen.GetCloseRequestObject) (apigen.GetCloseResponseObject, error) {
	return nil, errNotYet
}
