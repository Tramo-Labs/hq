# platform

Shared plumbing every module uses. Owned by backend. Business modules depend
on it; it depends on no business module.

| Package | What it gives you |
|---------|-------------------|
| `config` | One `Config` from environment variables, validated at startup. Secrets never printed. |
| `db` | Postgres pool, embedded goose migrations, `InTenant` and `System` transactions. |
| `db/dbtest` | Real migrated Postgres 17 for tests, without Docker. |
| `httpx` | Request id and client IP, `application/problem+json` errors, panic recovery, health, graceful shutdown. |
| `idempotency` | `Idempotency-Key` middleware (eng/ARQUITECTURA.md section 7). |
| `crypto` | Envelope encryption: AES-256-GCM data key per tenant, wrapped by the master key. |
| `audit` | Append only audit log, written in the same transaction as the action. |
| `mail` | Outbox file transport (0600) for invitation emails. No SMTP: nothing is sent. |

## How a module uses it

```go
// Every query of a business module runs in a tenant transaction. RLS
// filters by tenant even if the WHERE clause forgets to.
err := deps.DB.InTenant(ctx, func(tx pgx.Tx) error {
    q := store.New(tx)                       // sqlc
    if err := q.InsertThing(ctx, ...); err != nil { return err }
    return audit.Record(ctx, tx, audit.Entry{Action: "things.create", Subject: "things:" + id})
})
```

Tests of a module that touches the database:

```go
func TestMain(m *testing.M) { os.Exit(dbtest.Main(m)) }

func TestSomething(t *testing.T) {
    d := dbtest.New(t) // fresh database, every migration applied
    ...
}
```

Who is acting comes from `db.ScopeFrom(ctx)`: `TenantID`, `UserID` and
`Role`. The auth middleware fills it from the verified access token only;
nothing in the request (body, headers, query) can change it
(`identity.TestRequestCannotOverrideRole`). Never take a role or tenant from
the request. Inside `InTenant` the role is also `current_setting('app.role',
true)` for policies. The token lives 10 minutes: an action that must see a
demotion right away re-reads the membership in its transaction.

New tables (migrations 00100 to 00199 for datos) must have `tenant_id uuid not null
references tenants(id)` and:

```sql
alter table things enable row level security;
create policy things_tenant on things to tramo_app
  using (tenant_id = app_current_tenant())
  with check (tenant_id = app_current_tenant());
```

`tramo_app` gets select, insert, update and delete on new tables automatically
(default privileges in 00002). Without the policy it sees no rows at all.

Also `alter table things force row level security;`: a test fails for any
table without it (`TestEveryTableHasForcedRLS`). Views must be created
`with (security_invoker = true)`, and materialized views are not allowed
(their stored rows bypass RLS): `TestEveryViewIsSecurityInvokerAndNoMaterializedViews`.

In production the pool logs in as `tramo_runtime` (migration 00004, ADR
0005): it owns nothing and has no table privileges, so the raw pool is
useless by design; migrations run separately with `api migrate` and a
privileged URL.

`System` runs as `tramo_system`, which reaches only the seven platform and
identity tables granted in migration 00003 and nothing a module adds later.
Only `internal/platform/db`, `internal/platform/idempotency` and
`internal/identity` may call it; `TestSystemCallersAreAllowlisted` fails
otherwise. Tests may use it to seed. See ADR 0002.

## Endpoints

`GET /healthz` (204) and `GET /readyz` (204, or 503 problem when the database
does not answer). Both in `api/openapi.yaml`, tag `platform`. Swagger UI in `/docs/`.

## Tables (migrations 00001 to 00003)

| Table | RLS for tramo_app | Notes |
|-------|-------------------|-------|
| `tenants` | own row only | |
| `users` | self or members of the current tenant; only `id`, `email`, `created_at` readable | `password_hash` and `totp_secret` are not readable by `tramo_app` |
| `memberships` | current tenant, or the caller's own memberships | |
| `refresh_tokens` | no access | identity only, through `System` (`tramo_system`) |
| `invites` | no access | identity only, through `System`; token hashes only |
| `audit_log` | read and insert for the current tenant | UPDATE and DELETE rejected by trigger, for every role |
| `idempotency_requests` | current tenant | middleware uses `System` with explicit scope |
| `tenant_keys` | read own key | wrapped data keys |

## Jobs

`idempotency.Purge` deletes keys older than 24 hours. The worker schedules it.

## Sensitive data

- `MASTER_KEY` and `JWT_SIGNING_KEY` come only from the environment, base64 of
  32 bytes (`openssl rand -base64 32`). Required outside dev; in dev a missing
  one is generated per run and logged as ephemeral (name only).
- Tenant data keys are stored only wrapped. Ciphertexts are bound to tenant
  and purpose, so a blob copied to another row or tenant does not decrypt.
- Idempotent responses are stored in clear in `idempotency_requests`:
  handlers behind an `Idempotency-Key` must never return tokens or keys.
- CORS (`httpx.CORS`, ADR 0007): only the exact origins in
  `CORS_ALLOWED_ORIGINS` get CORS headers, echoed with credentials, never
  `*`; wildcards, paths and `null` are refused at startup, and http outside
  dev. A foreign origin gets no CORS header and its preflight is a 403
  before the router (`TestCORSGivesForeignOriginsNothing`,
  `TestCORSOnTheRouter`; mutations checked: allowing any origin, letting a
  foreign preflight through and unwiring the middleware each fail a test).
- Problem details never carry submitted values, personal data or amounts.
  `readyz` logs the cause and answers without it.
- The audit log stores IP and request id, never bodies. `Detail` must not
  carry secrets, third party CUIT or amounts.
- Every 403 is audited (`authz.denied`, spec §1): a strict middleware in
  `internal/app` writes it in its own tenant transaction with the caller,
  resolved IP, request id, the operationId as subject (`operation:<id>`)
  and role, method and path as detail; never the body or query string. If
  that write fails the caller still gets the 403 and the failure is logged.
  Rows are not throttled: a member hammering a forbidden operation grows
  their own tenant's log (`TestEveryDenialIsAudited`; mutations checked:
  without the middleware the test fails, auditing only the module 403s
  misses identity and portal ones).
- `X-Forwarded-For` is believed only when the TCP peer is in
  `TRUSTED_PROXIES` (our own proxies). Then its hops are read from the
  right, skipping those that are also our proxies; the first hop that is not
  ours is the client, and everything to its left (written by the client) is
  ignored. A hop that does not parse stops the walk at the last proxy.
  **In production `TRUSTED_PROXIES` is only our Caddy** (D-18, ADR 0007):
  the browser calls api.X directly, so Caddy's peer is the browser and
  Caddy replaces any client sent `X-Forwarded-For` with it. The web's Next
  server is not on the production path and is never listed. Its same origin
  `/v1` proxy exists in dev only; there, listing it (and having it append
  the browser's address) is what keeps two people behind it on separate
  throttles, which is why the walk skips every trusted hop and not just
  one (`TestClientsBehindTheWebProxyCountSeparately`: trusted, an attacker
  behind the web is throttled and the owner behind it still logs in; not
  trusted, the owner is throttled too. Mutation checked: ignoring the
  header from a trusted proxy fails it. `TestForwardedForOnlyFromTrustedProxies`
  covers two proxies in a row, and a client spoofing hops behind both). A client reaching the api directly cannot choose its
  IP, so it cannot dodge per IP throttles or forge the IP in the audit log
  (`TestForwardedForOnlyFromTrustedProxies`,
  `TestSpoofedForwardedForDoesNotDodgeThrottles`). The old
  `TRUST_PROXY_HEADERS` switch, which trusted any peer, is refused at
  startup. Trusted proxy ranges broader than /8 (IPv4) or /32 (IPv6) are
  refused; IPv4-mapped entries (`::ffff:...`) are judged and matched as the
  IPv4 range they cover. `TestAuditStoresTheResolvedIP` proves the audit
  log stores the resolved IP, not a spoofed header.

## Errors

Every error is RFC 9457 with a stable `code`: `bad_request`, `unauthorized`,
`forbidden`, `not_found`, `conflict`, `idempotency_key_required`,
`idempotency_key_reused` (422), `idempotency_key_in_flight` (409),
`payload_too_large`, `rate_limited`, `internal`.

A 400 from spec validation or parameter binding has one short, stable
`detail`: `invalid parameter "<name>"`, `invalid request body: /<field>`,
`invalid request body: unknown property` or `invalid request body`
(`internal/app/errors.go`). The library's own message is never sent: it
printed the whole schema and the submitted value, a password included.
Tested by `TestBadRequestDetailIsShortAndStable` (query, path, enum, body
pattern, unknown and missing field; mutations checked: restoring the
library message on either the validator or the router path fails it).

## How it is tested

`make test` runs everything with `-race` on an embedded Postgres (first run
downloads the Postgres 17 binaries to `~/.embedded-postgres-go`). `make
test-short` skips the database tests.

- `db`: a tenant cannot read or write another tenant's rows with no WHERE
  clause; `tramo_app` cannot read password hashes or refresh tokens; a tenant
  transaction without scope fails; the audit log rejects UPDATE and DELETE.
  Removing `SET ROLE` from `InTenant` makes three of these fail (checked).
  The application roles end up `NOSUPERUSER NOBYPASSRLS` even when they
  existed as superusers before migrating; every table in every non system
  schema (module schemas included) has forced RLS; the
  system role cannot read a business table; only allowlisted packages
  reference `System` (ADR 0002).
- `idempotency`: replay, 422 on another body, 409 while in flight, 5xx frees
  the key, 4xx is stored, keys are per tenant, expiry after 24 hours. For
  multipart uploads the hash covers each part (field, file name, content
  type, content), not the raw bytes: a browser retrying the same file with a
  new boundary replays instead of getting 422, while another file, file
  name or field under the same key is still 422; malformed multipart is 400.
- `crypto`: round trip, tamper, wrong key, wrong purpose, wrong tenant, wrong
  master key.
- `audit`: who, request id and IP are filled; tenants cannot write or read
  another tenant's trail.
- `config`, `httpx`: secrets never printed, bad values rejected, proxy
  headers not trusted by default, panics answered without detail.
- `internal/app`: the assembled router, and two rules on the spec: every POST
  that returns 201 requires `Idempotency-Key`, and every error response uses
  `Problem`.
