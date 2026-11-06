# app

Assembles the core HTTP API: the router generated from `api/openapi.yaml`,
request validation against that same spec, the platform middleware chain
and the handlers of every module (identity, ledger, documents, matching,
portal, closes). It owns no business logic and no table. Owned by backend.

## Request path

Outermost first:

`RequestMeta -> Recover -> CORS -> refresh Origin check -> Authenticate -> spec validation -> Idempotency -> audit of denials -> handler`

- **RequestMeta** gives each request an id and the client IP (only
  `TrustedProxies` may set `X-Forwarded-For`).
- **Authenticate** reads a bearer token when present and puts the principal
  and the tenant scope in the context. Whether an operation needs one is
  the spec's: every operation requires the bearer scheme unless it says
  `security: []`.
- **Validation** runs before idempotency, so a malformed or anonymous
  request never claims a key. A 400 names the field at fault in one short
  line and never echoes the value or the schema (F17, `errors.go`).
- **Idempotency** (`internal/platform/idempotency`): every POST that
  creates requires `Idempotency-Key`; keys are scoped to the caller's
  tenant and replay the first answer for 24 hours.
- **Audit of denials**: every 403 writes one `authz.denied` audit row with
  the caller, the IP, the operation and the resource, never the body;
  repeats of one user on one operation within `DenialWindow` count on the
  first row.

## Endpoints

None of its own besides `GET /healthz` and `GET /readyz` (database ping).
Everything else is a module's; see each module's README. `Deps` carries
what the modules share: the database, identity, trusted proxies, browser
origins (CORS, D-18), the docs directory, and for closes the period numbers
(`CloseNumbers`, nil until matching provides them) and the clock.

## Tables, jobs

None. Jobs are in `internal/worker`.

## Sensitive data

Passes through tokens, passwords and uploaded statements without keeping
them; logs carry the request id, never bodies. Error details never carry
personal data, amounts or secrets.

## How it is tested

`go test ./internal/app/` runs the whole API over `httptest` against an
embedded Postgres: identity and sessions (rotation, reuse revokes the
family, cookies and origins), invitations and magic links, the role matrix
(`authz_test.go`), tenant isolation, throttles, the data paths end to end,
the portal views, the period close (`closes_test.go`), denials audited
once per window, and that every binary passes the browser origins
(`wiring_test.go`).
