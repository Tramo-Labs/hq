# portal

Read views for the client portal and the operator console. Owned by backend.
Owns no table: it reads through identity, ledger and matching, always under
the tenant scope of the access token. No operation takes a tenant id from the
request.

## Endpoints (api/openapi.yaml, tag `portal`)

| Method and path | Who | What |
|-----------------|-----|------|
| `GET /v1/me/tenant` | any member | Onboarding status of the active tenant: three steps and open exceptions. |
| `GET /v1/tenants` | operator or accountant | The same status for every tenant where the caller has one of those roles; 403 otherwise. |
| `GET /v1/metrics?period=YYYY-MM` | any member | Results panel of the month, computed by `matching.PGStore.Metrics`; `no_data` (null value) when there is nothing to measure. Definitions in internal/matching/README.md, "Metrics". |
| `GET /v1/exports` | owner, accountant, operator | Downloads available, with row counts. |
| `GET /v1/exports/{kind}` | owner, accountant, operator | CSV of `movements`, `matches` (rows from `matching.PGStore.MatchRows`) or `audit`, optional `from`/`to`. |

### Onboarding steps

- `arca`: always `pending`. A CUIT on file is not a verification, and a
  constancia lookup proves the CUIT exists, not that the client delegated
  anything (producto's note). Becomes `verified` only when a delegation check
  exists and its result is stored.
- `bank`: `verified` once any account has an accepted statement (a
  quarantined one does not count). Read through `ledger.PGStore.LastStatement`.
- `erp`: `pending`; there is no ERP connector yet.
- `openExceptions`: undecided matches plus movements without candidate in
  the Exceptions tray (`matching.PGStore.Undecided` and `LatestPending`),
  each capped at 1000; `openExceptionsCapped` says when the cap was hit.

### Console

`GET /v1/tenants` builds the list from the caller's own memberships
(`identity.ConsolePrincipals`) and reads each tenant with
`identity.WithPrincipal` for that membership, so RLS applies per tenant with
the role of that membership.

Exports, the audit export included, follow spec §1 ("Descargar todo" and
"Ver el audit log"): approver and viewer get 403 `forbidden` on both export
operations (`CanExport`).

## Tables, jobs, events

None of its own. Reads `tenants` (identity), `ledger.*` through the ledger
store, `matching.*` through the matching store, `audit_log` directly (the
tenant's own rows, under RLS).

## Sensitive data

- Exports contain the tenant's own movements (amounts, descriptions) and its
  audit trail (user ids, request ids, IPs). Only the tenant of the token gets
  them; nothing crosses tenants (tested).
- CSV cells starting with `=`, `+`, `@`, tab, carriage return, or `-` when
  not a number are prefixed with `'`, so a bank description cannot run as a
  spreadsheet formula. Negative amounts stay numbers.

## How it is tested

- `internal/app/portal_test.go`, end to end over HTTP: steps for a new
  tenant (all pending); an accepted statement verifies only the bank step; a
  quarantined statement does not; the console list holds exactly the
  operator's tenants with the operator role, an owner gets 403, and a
  `tenantId` in the query string is ignored; exports count and return the
  tenant's own movements (10 rows from the Bancor fixture), another tenant
  gets only the header, the audit export holds the tenant's trail and not
  the other's; an inverted range is 400; the matches export answers its
  header and metrics answer 200 before any run (metrics are tested in
  internal/matching and internal/app/data_test.go).
- `internal/app/authz_test.go`, `TestExportsAreOnlyForOwnerAccountantAndOperator`:
  every role against the list and the three kinds (F6). Mutations checked:
  letting approver through fails 4 cases; dropping the check from
  `GetExport` alone fails 6.
- `csv_test.go`: formula neutralization. Mutations checked: without it five
  cells fail; reading each console tenant without its own scope fails the
  console test.
