package mercadopago

import (
	"context"
	"errors"
	"net/http"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Handlers implements the mercadopago operations of the strict server.
type Handlers struct{ svc *Service }

// NewHandlers returns the handlers over the service.
func NewHandlers(s *Service) *Handlers { return &Handlers{svc: s} }

func problem(err error) error {
	e := func(status int, code, detail string) error {
		return &httpx.Error{Status: status, Code: code, Detail: detail}
	}
	switch {
	case errors.Is(err, ErrForbidden):
		return e(http.StatusForbidden, httpx.CodeForbidden, "only the owner connects Mercado Pago")
	case errors.Is(err, ErrNotConfigured):
		return e(http.StatusNotImplemented, "not_configured", "Mercado Pago is not configured yet")
	case errors.Is(err, ErrStateInvalid):
		return e(http.StatusBadRequest, "state_invalid", "this connection attempt is unknown; start again")
	case errors.Is(err, ErrStateNotYours):
		return e(http.StatusForbidden, "state_not_yours", "this connection attempt was started by someone else")
	case errors.Is(err, ErrStateExpired):
		return e(http.StatusConflict, "state_expired", "this connection attempt expired; start again")
	case errors.Is(err, ErrStateUsed):
		return e(http.StatusConflict, "state_used", "this connection attempt was already used")
	case errors.Is(err, ErrCodeRejected):
		return e(http.StatusUnprocessableEntity, "mp_code_rejected", "Mercado Pago did not accept the authorization; start again")
	case errors.Is(err, ErrMPUnavailable):
		return e(http.StatusBadGateway, "mp_unavailable", "Mercado Pago did not answer; try again in a few minutes")
	}
	return err
}

func toAPI(s Status) apigen.MercadoPagoConnection {
	out := apigen.MercadoPagoConnection{Configured: s.Configured, Status: apigen.MercadoPagoConnectionStatus(s.Status),
		ConnectedAt: s.ConnectedAt, ConnectedBy: s.ConnectedBy, TokenExpiresAt: s.TokenExpiresAt,
		LastRefreshAt: s.LastRefreshAt, LastErrorAt: s.LastErrorAt, RevokeInMercadoPago: s.RevokeInMP}
	if s.MPUserID != "" {
		id := s.MPUserID
		out.MpUserId = &id
	}
	if s.LastErrorCode != "" {
		c := apigen.MercadoPagoConnectionLastErrorCode(s.LastErrorCode)
		out.LastErrorCode = &c
	}
	return out
}

// GetMercadoPago returns the connection of the active tenant.
func (h *Handlers) GetMercadoPago(ctx context.Context, _ apigen.GetMercadoPagoRequestObject) (apigen.GetMercadoPagoResponseObject, error) {
	st, err := h.svc.Get(ctx)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.GetMercadoPago200JSONResponse(toAPI(st)), nil
}

// AuthorizeMercadoPago starts a connection (owner).
func (h *Handlers) AuthorizeMercadoPago(ctx context.Context, _ apigen.AuthorizeMercadoPagoRequestObject) (apigen.AuthorizeMercadoPagoResponseObject, error) {
	u, exp, err := h.svc.Authorize(ctx)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.AuthorizeMercadoPago201JSONResponse{AuthorizeUrl: u, ExpiresAt: exp}, nil
}

// ConnectMercadoPago finishes a connection (owner).
func (h *Handlers) ConnectMercadoPago(ctx context.Context, req apigen.ConnectMercadoPagoRequestObject) (apigen.ConnectMercadoPagoResponseObject, error) {
	st, err := h.svc.Connect(ctx, req.Body.Code, req.Body.State)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.ConnectMercadoPago201JSONResponse(toAPI(st)), nil
}

// DisconnectMercadoPago forgets the connection (owner).
func (h *Handlers) DisconnectMercadoPago(ctx context.Context, _ apigen.DisconnectMercadoPagoRequestObject) (apigen.DisconnectMercadoPagoResponseObject, error) {
	st, err := h.svc.Disconnect(ctx)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.DisconnectMercadoPago200JSONResponse(toAPI(st)), nil
}
