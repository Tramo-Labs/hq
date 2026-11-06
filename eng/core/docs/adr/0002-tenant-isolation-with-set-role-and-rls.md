# 0002: Tenant isolation with SET LOCAL ROLE and row level security

Date: 2026-11-06. Status: accepted.

Context: every business table carries `tenant_id` and the rule is that a bug
in code cannot cross tenants. RLS does nothing for superusers or for the
owner of a table, and the compose Postgres user (like most managed setups)
owns the schema because it runs the migrations.

Decision: migration 00002 creates a `NOLOGIN` role `tramo_app` without
`BYPASSRLS`, grants it to the migrating user, and sets default privileges so
it can use every future table. Each request scoped to a tenant runs in a
transaction that does `SET LOCAL ROLE tramo_app` and sets `app.tenant_id` and
`app.user_id` with `set_config(..., true)`. Policies compare against
`app_current_tenant()`. Both settings and the role reset at commit, so pooled
connections never leak a scope. Modules only get `db.InTenant`; `db.System`
(owner, bypasses RLS) is reserved for platform and identity.

Column privileges hide `users.password_hash` and `users.totp_secret` from
`tramo_app`, and it has no access to `refresh_tokens`.

Alternatives: a separate login role for the pool (needs a second secret and
two pools for system work); `FORCE ROW LEVEL SECURITY` on the owner (does not
apply to superusers, and migrations would then need the tenant set); schema
per tenant (migrations multiply by tenants, cross tenant reporting for the
accountant role gets hard).

Consequences: a query that forgets `WHERE tenant_id` still sees one tenant
(tested by removing the `SET ROLE` and watching three tests fail). Every new
table needs `enable row level security` plus a policy, or `tramo_app` sees no
rows: the failure mode is empty, not leaky. One extra round trip per
transaction for the role and settings.

## Revision, 2026-11-06 (migration 00003)

QA pointed out that `db.System` ran as the owner and therefore saw every
tenant, so "a code bug cannot cross tenants" held only for code that chose
`InTenant`. Decided:

- `db.System` now does `SET LOCAL ROLE tramo_system`. That role reaches only
  the seven platform and identity tables granted in 00003, each with a
  written reason (registration, login, refresh tokens, idempotency, tenant
  keys, audit). It gets nothing on tables added later: a cross tenant job
  needs its own grants and policy in a migration that says why.
- Which packages may reference `System` is an allowlist
  (`internal/platform/db`, `internal/platform/idempotency`,
  `internal/identity`, and `internal/devseed` for the dev only seed of
  `make dev`, which refuses any environment but dev; and, since 2026-11-06,
  `internal/worker` for the monthly fan out, which lists tenant ids and
  writes its durable mark `platform.month_closes` (migration 00115) in one
  transaction so a closed month is never run twice), enforced by `TestSystemCallersAreAllowlisted`, which
  parses the source tree and flags any `.System` selector outside it: calls,
  method values (`f := d.System`) and method expressions. It matches by name,
  not by type, so it is stricter than needed. Adding an entry means adding a
  line here.
- Since 2026-11-06 a third role exists for one lookup before any tenant
  scope: `tramo_inbox` (migration 00201, ADR 0009) may only execute
  `inbox.address_tenant`, which returns the tenant of a mailbox address.
  It has no table privileges, and `db.InboxTenant` runs that one fixed
  query, so no caller can use the role for anything else
  (`TestTheLookupRoleReadsNothingButTheFunction` checks every table and
  function in the database). It is not `tramo_system` and not on the list
  above.
- Every table has `FORCE ROW LEVEL SECURITY`; `TestEveryTableHasForcedRLS`
  fails for any table that lacks it in any schema except `pg_catalog`,
  `information_schema`, `pg_toast` and temporary schemas (so module schemas
  such as `ledger` or `matching` are covered; only goose's own version table
  is exempt). Checked by mutation: a table without RLS in a new schema fails
  it. Note: the default privileges of 00002 cover `public` only; a module
  that creates its own schema grants `tramo_app` usage and table rights in
  its migration. With
  a non superuser owner (prod, managed Postgres), a stray query on the raw
  pool sees no rows. A superuser owner still bypasses RLS. (Superseded by
  ADR 0005: a non superuser owner cannot run the migrations at all, so
  production migrates with a privileged URL in a separate step and runs the
  api as `tramo_runtime`, which owns nothing and has no table privileges.)
- When migration 00003 runs on a database, it alters both application roles
  to `NOSUPERUSER NOBYPASSRLS NOLOGIN`, also when they already existed with
  other attributes. Two tests cover it: `TestApplicationRolesCannotBypassRLS`
  checks the attributes after a normal migration, and
  `TestMigrationFixesPreexistingRoleAttributes` makes both roles
  `SUPERUSER BYPASSRLS LOGIN` before migrating an empty database and checks
  they end up false. Limit: roles are server wide and 00003 runs once per
  database, so someone altering a role by hand after that is not undone by
  later migrations; the first test catches it on the next test run, not in
  production.

Checked by mutation: a `System` call, and a `System` method value, added to
the router fail the allowlist test; `System` without the role switch fails
`TestSystemRoleCannotReachBusinessTables`; deleting both `alter role` lines
from 00003 fails `TestMigrationFixesPreexistingRoleAttributes` (before this
test existed, that mutation survived; QA found it).
