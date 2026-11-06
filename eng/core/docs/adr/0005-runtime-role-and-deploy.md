# 0005: Runtime login role, migrations as a separate step, deploy pipeline

Date: 2026-11-06. Status: accepted.

Context: ADR 0002 left a limit: the pool connects as the schema owner, and
a superuser owner bypasses RLS, so "production must connect as a non
superuser owner". We tested that (a probe against the embedded Postgres):
a non superuser owner cannot run our migrations at all. 00002 fails with
`permission denied to grant role "tramo_app"` and every `ALTER ROLE ...
NOSUPERUSER NOBYPASSRLS` of 00003 fails with `permission denied to alter
role`. Only a superuser may change those attributes.

Decision:

- **Two connections, two moments.** `api migrate` applies migrations with
  `MIGRATE_DATABASE_URL` (the privileged owner) and exits; it reads no other
  configuration, so those credentials never meet the app secrets. The api
  and the worker run with `DATABASE_URL` as **`tramo_runtime`**: LOGIN,
  NOSUPERUSER, NOBYPASSRLS, owns nothing, no privilege on any table, member
  of `tramo_app` and `tramo_system` only so it can `SET ROLE`. A stray query
  on the raw pool gets "permission denied"; data is reachable only through
  `InTenant` and the allowlisted `System`.
- `tramo_runtime` is created by the operator because it has a password
  (`deploy/postgres-init/10-runtime-role.sh` on first Postgres init, from
  `postgres.env`). Migration 00004 wires it when present (strips dangerous
  attributes, grants the two roles, `CONNECT`); dev and tests are unchanged.
- The api migrates on start only in dev (`MIGRATE_ON_START` defaults to
  true in dev, false elsewhere). Production compose runs a one shot
  `migrate` service before api and worker.
- **Secrets per container.** `postgres.env` (owner and runtime passwords,
  migrate URL), `core.env` (runtime URL, `MASTER_KEY`, `JWT_SIGNING_KEY`),
  `gateway.env` (`ARCA_CUIT`, `ARCA_CERT`, `ARCA_KEY`). None committed
  (`deploy/.gitignore`); names in `deploy/env-examples/`.
- **CI** (`.github/workflows/core.yml`): pinned tools, `make lint`,
  `make gen-check`, `make build`, `make test` (race, embedded Postgres),
  `make sec` (gosec and govulncheck, pinned); Terraform `fmt -check`,
  `init -backend=false` and `validate` (no credentials, no plan); on main,
  one image to GHCR tagged with the commit sha, using the workflow's own
  `GITHUB_TOKEN`.
- **Terraform** (`deploy/terraform`): Hetzner VPS with firewall (ssh only
  from operator CIDRs), Cloudflare A and AAAA records, an R2 bucket for
  encrypted backups. Tokens only from the environment. Local state until a
  remote backend is chosen. Provider versions locked in
  `.terraform.lock.hcl`.

Tested: `TestRuntimeLoginRoleReachesDataOnlyThroughTheRoleSwitch` creates
`tramo_runtime` as SUPERUSER BYPASSRLS, migrates, checks the attributes are
stripped, then as `tramo_runtime` the raw pool is denied on tenants, users
and audit_log while `InTenant` sees exactly one tenant (mutation: deleting
the `alter role` line of 00004 fails it). `TestMigrationDefaults` checks the
api does not migrate by itself outside dev.

Not done here (needs accounts or credentials; listed in
`deploy/terraform/README.md`): remote Terraform state, the deploy job that
runs `docker compose pull && up` on the VPS, backups to the bucket, mTLS
between core and the gateway, monitoring.
