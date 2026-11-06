package app

import (
	"context"
	"net/http"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// pendingHandlers answers operations declared in the spec whose code has
// not landed yet with 501. Each one is deleted when its module implements
// the operation.
type pendingHandlers struct{}

var errPending = &httpx.Error{Status: http.StatusNotImplemented, Code: "not_implemented",
	Detail: "not available yet; see the API description"}

func (pendingHandlers) RemoveMember(context.Context, apigen.RemoveMemberRequestObject) (apigen.RemoveMemberResponseObject, error) {
	return nil, errPending
}

func (pendingHandlers) AcceptInvite(context.Context, apigen.AcceptInviteRequestObject) (apigen.AcceptInviteResponseObject, error) {
	return nil, errPending
}
