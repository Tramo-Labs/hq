package httpx

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Origins is a closed list of exact browser origins (scheme://host[:port]).
// There is no wildcard: an origin is allowed only if it is on the list,
// byte for byte, as a browser serializes it.
type Origins struct{ set map[string]bool }

// ParseOrigins reads a comma separated list of origins. Each must be
// http(s)://host[:port] with nothing else: no path, query, user or "*".
// Outside dev (allowHTTP false) only https is accepted. Scheme and host are
// lowercased, which is how browsers send them.
func ParseOrigins(s string, allowHTTP bool) (Origins, error) {
	o := Origins{set: map[string]bool{}}
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		u, err := url.Parse(f)
		if err != nil || u.Host == "" || u.Opaque != "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			strings.Contains(u.Host, "*") {
			return Origins{}, fmt.Errorf("origin %q must be scheme://host[:port], exact", f)
		}
		scheme := strings.ToLower(u.Scheme)
		switch {
		case scheme == "https":
		case scheme == "http" && allowHTTP:
		default:
			return Origins{}, fmt.Errorf("origin %q: scheme must be https", f)
		}
		o.set[scheme+"://"+strings.ToLower(u.Host)] = true
	}
	return o, nil
}

// Allowed reports whether origin is on the list.
func (o Origins) Allowed(origin string) bool { return origin != "" && o.set[origin] }

// List returns the origins, for logs.
func (o Origins) List() []string {
	out := make([]string, 0, len(o.set))
	for k := range o.set {
		out = append(out, k)
	}
	return out
}

// What the web sends and reads (D-18): the access token, JSON bodies and
// the idempotency key; it reads Retry-After on a 429, the request id, and
// Content-Disposition for the file name of an export.
const (
	corsAllowMethods  = "GET, POST, PUT, PATCH, DELETE"
	corsAllowHeaders  = "Authorization, Content-Type, Idempotency-Key"
	corsExposeHeaders = "Retry-After, X-Request-Id, Content-Disposition"
	corsMaxAge        = "600"
)

// CORS lets the listed origins call the api from the browser with
// credentials (the refresh cookie). A listed origin is echoed exactly,
// never "*". Any other origin gets no CORS header at all, so the browser
// does not let its page read the answer; its preflights are refused here
// with 403 and never reach the api. Requests without Origin (curl, server
// to server) pass untouched: CORS is a browser rule, not authentication.
func CORS(origins Origins) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Origin")
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			if !origins.Allowed(origin) {
				if preflight {
					WriteProblem(w, r, http.StatusForbidden, "origin_not_allowed", "")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			if preflight {
				h.Set("Access-Control-Allow-Methods", corsAllowMethods)
				h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
				h.Set("Access-Control-Max-Age", corsMaxAge)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			h.Set("Access-Control-Expose-Headers", corsExposeHeaders)
			next.ServeHTTP(w, r)
		})
	}
}
