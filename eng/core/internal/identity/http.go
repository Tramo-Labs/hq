package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/db"
	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// RefreshCookie is the cookie that carries the refresh token in browsers.
const RefreshCookie = "tramo_refresh"

type principalKey struct{}

// PrincipalFrom returns the authenticated caller, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// WithPrincipal stores p and its tenant scope, role included, in ctx. The
// scope it writes replaces any scope already in the context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	ctx = context.WithValue(ctx, principalKey{}, p)
	return db.WithScope(ctx, db.Scope{TenantID: p.TenantID, UserID: p.UserID, Role: p.Role})
}

// Authenticate verifies a bearer access token when one is sent. A bad token
// is a 401 right away; no token leaves the request anonymous and the spec
// decides whether the operation allows that.
func Authenticate(s *Signer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" {
				next.ServeHTTP(w, r)
				return
			}
			tok, ok := strings.CutPrefix(h, "Bearer ")
			if !ok {
				unauthorized(w, r)
				return
			}
			p, err := s.Verify(strings.TrimSpace(tok))
			if err != nil {
				unauthorized(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="tramo", error="invalid_token"`)
	httpx.WriteProblem(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid or expired access token")
}

// Handlers implements the identity operations of the generated strict
// server interface.
type Handlers struct{ s *Service }

// NewHandlers wraps the service.
func NewHandlers(s *Service) *Handlers { return &Handlers{s: s} }

// Register creates an account and its first tenant.
func (h *Handlers) Register(ctx context.Context, req apigen.RegisterRequestObject) (apigen.RegisterResponseObject, error) {
	b := req.Body
	in := RegisterInput{Email: string(b.Email), Password: b.Password, TenantName: b.TenantName, TenantSlug: b.TenantSlug}
	if b.Cuit != nil {
		in.Cuit = *b.Cuit
	}
	user, tenant, err := h.s.Register(ctx, in)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.Register201JSONResponse{UserId: user, TenantId: tenant}, nil
}

// Login opens a session.
func (h *Handlers) Login(ctx context.Context, req apigen.LoginRequestObject) (apigen.LoginResponseObject, error) {
	b := req.Body
	sess, err := h.s.Login(ctx, string(b.Email), b.Password, (*uuid.UUID)(b.TenantId))
	if err != nil {
		return nil, problem(err)
	}
	return apigen.Login200JSONResponse{TokensJSONResponse: tokens(sess, b.TokenDelivery)}, nil
}

// AcceptInvite sets the password with an invitation and opens a session.
func (h *Handlers) AcceptInvite(ctx context.Context, req apigen.AcceptInviteRequestObject) (apigen.AcceptInviteResponseObject, error) {
	sess, err := h.s.AcceptInvite(ctx, req.Body.Token, req.Body.Password)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.AcceptInvite200JSONResponse{TokensJSONResponse: tokens(sess, req.Body.TokenDelivery)}, nil
}

// Refresh rotates the refresh token.
func (h *Handlers) Refresh(ctx context.Context, req apigen.RefreshRequestObject) (apigen.RefreshResponseObject, error) {
	tok, delivery := refreshInput(req.Params.TramoRefresh, req.Body)
	sess, err := h.s.Refresh(ctx, tok)
	if err != nil {
		return nil, problem(err)
	}
	return apigen.Refresh200JSONResponse{TokensJSONResponse: tokens(sess, delivery)}, nil
}

// Logout revokes the session and clears the cookie.
func (h *Handlers) Logout(ctx context.Context, req apigen.LogoutRequestObject) (apigen.LogoutResponseObject, error) {
	tok, _ := refreshInput(req.Params.TramoRefresh, req.Body)
	if err := h.s.Logout(ctx, tok); err != nil {
		return nil, err
	}
	clear := (&http.Cookie{Name: RefreshCookie, Value: "", Path: "/v1/auth", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String()
	return apigen.Logout204Response{Headers: apigen.Logout204ResponseHeaders{SetCookie: &clear}}, nil
}

// GetMe returns the caller.
func (h *Handlers) GetMe(ctx context.Context, _ apigen.GetMeRequestObject) (apigen.GetMeResponseObject, error) {
	p, _ := PrincipalFrom(ctx)
	v, err := h.s.Me(ctx, p)
	if err != nil {
		return nil, err
	}
	out := apigen.GetMe200JSONResponse{
		UserId: p.UserID, Email: v.Email, ActiveTenantId: p.TenantID, Role: apigen.Role(p.Role),
		Memberships: make([]apigen.Membership, 0, len(v.Memberships)),
	}
	for _, m := range v.Memberships {
		out.Memberships = append(out.Memberships, apigen.Membership{TenantId: m.TenantID, TenantName: m.TenantName, Role: apigen.Role(m.Role)})
	}
	return out, nil
}

// ListMembers lists the members of the active tenant.
func (h *Handlers) ListMembers(ctx context.Context, _ apigen.ListMembersRequestObject) (apigen.ListMembersResponseObject, error) {
	rows, err := h.s.ListMembers(ctx)
	if err != nil {
		return nil, err
	}
	out := apigen.ListMembers200JSONResponse{Items: make([]apigen.Member, 0, len(rows))}
	for _, m := range rows {
		out.Items = append(out.Items, apigen.Member{UserId: m.ID, Email: m.Email, Role: apigen.Role(m.Role)})
	}
	return out, nil
}

// AddMember adds a person to the active tenant.
func (h *Handlers) AddMember(ctx context.Context, req apigen.AddMemberRequestObject) (apigen.AddMemberResponseObject, error) {
	p, _ := PrincipalFrom(ctx)
	m, err := h.s.AddMember(ctx, p, string(req.Body.Email), string(req.Body.Role))
	if err != nil {
		return nil, problem(err)
	}
	return apigen.AddMember201JSONResponse{UserId: m.ID, Email: m.Email, Role: apigen.Role(m.Role)}, nil
}

// RemoveMember takes a person out of the active tenant.
func (h *Handlers) RemoveMember(ctx context.Context, req apigen.RemoveMemberRequestObject) (apigen.RemoveMemberResponseObject, error) {
	p, _ := PrincipalFrom(ctx)
	if err := h.s.RemoveMember(ctx, p, req.UserId); err != nil {
		return nil, problem(err)
	}
	return apigen.RemoveMember204Response{}, nil
}

func refreshInput(cookie *string, body *apigen.RefreshRequest) (string, *apigen.TokenDelivery) {
	tok := ""
	var delivery *apigen.TokenDelivery
	if body != nil {
		delivery = body.TokenDelivery
		if body.RefreshToken != nil {
			tok = *body.RefreshToken
		}
	}
	if tok == "" && cookie != nil {
		tok = *cookie
	}
	return tok, delivery
}

func tokens(s Session, delivery *apigen.TokenDelivery) apigen.TokensJSONResponse {
	out := apigen.TokensJSONResponse{Body: apigen.Tokens{
		AccessToken: s.Access, TokenType: apigen.Bearer, ExpiresIn: int(AccessTTL.Seconds()),
		TenantId: s.Principal.TenantID, Role: apigen.Role(s.Principal.Role),
	}}
	if delivery != nil && *delivery == apigen.Body {
		out.Body.RefreshToken = &s.Refresh
		return out
	}
	c := (&http.Cookie{Name: RefreshCookie, Value: s.Refresh, Path: "/v1/auth", MaxAge: int(RefreshTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}).String()
	out.Headers.SetCookie = &c
	return out
}

// problem maps service errors to stable problem codes. Unknown errors stay
// as they are and become a 500 without detail.
func problem(err error) error {
	var rl *RateLimitedError
	if errors.As(err, &rl) {
		secs := int(rl.RetryAfter.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		return &httpx.Error{Status: http.StatusTooManyRequests, Code: httpx.CodeRateLimited,
			Detail: "too many attempts; try again later", RetryAfter: secs}
	}
	type m struct {
		status int
		code   string
	}
	table := map[error]m{
		ErrInvalidCredentials: {http.StatusUnauthorized, httpx.CodeUnauthorized},
		ErrInvalidRefresh:     {http.StatusUnauthorized, httpx.CodeUnauthorized},
		ErrRefreshReused:      {http.StatusUnauthorized, "refresh_token_reused"},
		ErrEmailTaken:         {http.StatusConflict, "email_taken"},
		ErrSlugTaken:          {http.StatusConflict, "slug_taken"},
		ErrCuitTaken:          {http.StatusConflict, "cuit_taken"},
		ErrAlreadyMember:      {http.StatusConflict, "already_member"},
		ErrInvalidEmail:       {http.StatusBadRequest, "invalid_email"},
		ErrInvalidCuit:        {http.StatusBadRequest, "invalid_cuit"},
		ErrForbidden:          {http.StatusForbidden, httpx.CodeForbidden},
		ErrNotAMember:         {http.StatusForbidden, "not_a_member"},
		ErrMemberNotFound:     {http.StatusNotFound, httpx.CodeNotFound},
		ErrLastOwner:          {http.StatusConflict, "last_owner"},
		ErrInvalidInvite:      {http.StatusUnauthorized, "invalid_invite"},
	}
	for target, p := range table {
		if errors.Is(err, target) {
			return &httpx.Error{Status: p.status, Code: p.code, Detail: target.Error()}
		}
	}
	return err
}
