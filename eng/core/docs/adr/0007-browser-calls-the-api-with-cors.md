# 0007: The browser calls the api directly, with a closed CORS list

Date: 2026-11-06. Status: accepted (D-18, pending Martín's confirmation).
Supersedes the same origin `/v1` proxy as the production path.

Context: until now the web's Next server proxied `/v1` same origin, so the
api had no CORS. That made the Next server a hop the api had to trust for
the client IP (login throttles, audit), and a Vercel deploy has no fixed
egress address to trust. D-18: the portal and the console call the core
api from the browser; web and api are subdomains of one registrable domain
(app.X and api.X, https only), so the `SameSite=Strict` refresh cookie still
travels (same site), and the client IP is what our own Caddy sees.

Decisions:

1. **Closed list of exact origins** (`CORS_ALLOWED_ORIGINS`): scheme, host
   and port, nothing else. No `*`, no wildcard subdomains, no `null`; https
   only outside dev. Refused at startup otherwise. In dev the default is
   `APP_BASE_URL`'s origin; elsewhere there is no default and an empty list
   means no browser can read an answer.
2. **Echo the origin, with credentials.** `Access-Control-Allow-Origin` is
   the request's origin when listed, `Allow-Credentials: true`,
   `Vary: Origin` on every response with an Origin.
3. **Only what the web uses.** Request headers `Authorization`,
   `Content-Type`, `Idempotency-Key`; exposed `Retry-After` (read on a 429)
   and `X-Request-Id`. Methods GET, POST, PUT, PATCH, DELETE. Preflights
   are answered by the middleware (204) and cached 10 minutes.
4. **A foreign origin gets nothing.** No CORS header on its responses; its
   preflights are refused with 403 `origin_not_allowed` before the router.
   Requests with no Origin (curl, server to server) pass untouched: CORS is
   a browser rule, not authentication; the bearer token still is.
5. **The same list is the Origin check on refresh and logout** (F22), the
   two operations a cookie alone can authorize.

Consequences: `TRUSTED_PROXIES` in prod is only our Caddy. The client IP
walk that skips trusted hops stays: in dev the Next proxy still exists.
The `/v1` proxy of the web is for dev only. Not yet run against real
browsers on real subdomains; the tests use the HTTP semantics only.

Amendment, 2026-11-06: `Content-Disposition` is exposed too. Without it the
browser cannot read the file name of an export and saves it under a generic
name (found by frontend; `TestExportFileNameIsReadableFromThePortal`).
