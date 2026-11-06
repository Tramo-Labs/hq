package identity

import (
	"net/http"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// cookieAuthorized are the operations the refresh cookie alone authorizes:
// a browser attaches it on its own, so a page on another origin could fire
// them (F22).
var cookieAuthorized = map[string]bool{
	"/v1/auth/refresh":       true,
	"/v1/auth/logout":        true,
	"/v1/auth/switch-tenant": true,
}

// RequireAllowedOrigin refuses refresh and logout carrying the refresh
// cookie unless the request's Origin is on the CORS list (ADR 0007). A
// missing Origin is refused too: browsers send it on every POST, so only a
// non browser client omits it, and such a client sends its token in the
// body, not in a cookie. The check runs before the handler, so a refused
// call neither rotates nor revokes the token.
func RequireAllowedOrigin(origins httpx.Origins) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && cookieAuthorized[r.URL.Path] && hasRefreshCookie(r) &&
				!origins.Allowed(r.Header.Get("Origin")) {
				httpx.WriteProblem(w, r, http.StatusForbidden, "origin_not_allowed", "")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasRefreshCookie(r *http.Request) bool {
	c, err := r.Cookie(RefreshCookie)
	return err == nil && c.Value != ""
}
