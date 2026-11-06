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
- Which packages may call `System` is an allowlist (`internal/platform/db`,
  `internal/platform/idempotency`, `internal/identity`), enforced by
  `TestSystemCallersAreAllowlisted`, which parses the source tree. Adding an
  entry means adding a line here.
- Every table has `FORCE ROW LEVEL SECURITY`; `TestEveryTableHasForcedRLS`
  fails for any table, including future module tables, that lacks it. With
  a non superuser owner (prod, managed Postgres), a stray query on the raw
  pool sees no rows. A superuser owner still bypasses RLS, so production
  must connect as a non superuser owner; the raw pool is used only to
  migrate.
- Both application roles are altered to `NOSUPERUSER NOBYPASSRLS NOLOGIN`
  on every migration run, also when they pre-existed;
  `TestApplicationRolesCannotBypassRLS` checks the attributes.

Checked by mutation: a `System` call added to the router fails the
allowlist test, and `System` without the role switch fails
`TestSystemRoleCannotReachBusinessTables`.
