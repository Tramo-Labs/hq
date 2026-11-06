# core

Backend of the product: REST API (OpenAPI first), background worker and the
ARCA gateway. Design and rules in `../ARQUITECTURA.md`; decisions in
`docs/adr/`.

```
make build       # compiles the three binaries
make test        # go test -race, database tests on an embedded Postgres 17
make test-short  # same without the database tests
make lint        # gofmt, go vet, buf lint
make tools       # installs the pinned generators into ./bin
make gen         # regenerates gen/ from api/openapi.yaml and proto/
make sec         # gosec + govulncheck (make tools-sec installs them)
make dev         # whole core locally, no Docker (see below)
make up          # docker compose: postgres, api, worker, arca-gateway, mailpit
```

## Run it locally: `make dev`

One command, no Docker: embedded Postgres 17 on port 54320, migrations,
a synthetic seed, the api on http://127.0.0.1:8080 (Swagger UI at `/docs/`),
the job worker, and the ARCA gateway on 127.0.0.1:9090 answering from the
simulator. Ctrl-C stops everything, Postgres included. The first run
downloads the Postgres binaries.

- **Dev only.** It refuses to start if `APP_ENV` is anything but `dev` (or
  unset) or `ARCA_ENV` anything but `sim`. The Makefile does not set them, so
  a shell with `APP_ENV=prod` is refused, not overridden.
- **Data survives restarts** in `DEV_DATA_DIR` (default
  `~/.local/share/tramo-dev`, outside the repo). Delete it to start over.
- **The seed is idempotent**: a second run adds nothing (tested). It uses
  datos' own code: the Bancor fixture goes through the ledger ingester and a
  matching run through the matching engine.
- **Passwords** are random per data directory, stored in
  `DEV_DATA_DIR/dev-secrets.json` (mode 0600, never in the repo) and printed
  at startup. The same file keeps the dev master key and JWT key, so data and
  sessions survive restarts.
- Ports: `DEV_PG_PORT`, `DEV_HTTP_ADDR`, `DEV_GRPC_ADDR` override them.
- **Emails are never sent.** Invitations land in `DEV_DATA_DIR/outbox`
  (mode 0600); the link inside points at `DEV_APP_BASE_URL` (default
  http://localhost:3000). There is no SMTP transport in the code.

Seeded data (all synthetic):

| Email | Role |
|-------|------|
| owner@alfa.test | owner of Molino Alfa (dev) |
| approver@alfa.test | approver of Molino Alfa (dev) |
| viewer@alfa.test | viewer of Molino Alfa (dev) |
| accountant@estudio.test | accountant of both tenants |
| operator@tramo.test | operator of both tenants |
| owner@beta.test | owner of Molino Beta (dev) |

Tenants: Molino Alfa (dev), CUIT 30000000015, and Molino Beta (dev), CUIT
30000000023. Each has a Bancor account with the October 2026 statement (10
movements) and a matching run for 2026-10. Every tray has items: 1
automatic, 1 suggested match plus 2 suggested movements, 6 exceptions. The
onboarding shows ARCA pending, bank verified, ERP pending.

Browsers call the api directly (D-18, ADR 0007): in production the portal
at app.X calls api.X, with CORS restricted to the exact origins in
`CORS_ALLOWED_ORIGINS` (never `*`). The refresh cookie is `SameSite=Strict`
and still travels because both are on one registrable domain. In dev the
allowed origin defaults to `APP_BASE_URL` (`DEV_APP_BASE_URL` under `make dev`, http://localhost:3000), or `CORS_ALLOWED_ORIGINS` when set. The
web's same origin `/v1` proxy is for dev only; it is not the production
path.

Deploy (ADR 0005): CI publishes `ghcr.io/<owner>/tramo-core:<sha>` on main.
On a server, `deploy/docker-compose.prod.yml` runs a one shot `api migrate`
with the privileged URL, then api and worker as `tramo_runtime`, the gateway
with its own env file, and Caddy. Infra in `deploy/terraform` (what still
needs accounts and credentials is listed in its README).

**Not yet run:** the Docker image build, both compose files, the CI workflow,
gosec and govulncheck have never run (no Docker on the dev machine, nothing
pushed). Terraform fmt and validate ran once locally, not in CI or qa. Full
table in ADR 0005, "What has NOT run". `make test` is what is proven.

Swagger UI: http://localhost:8080/docs/ once the api is up.

Environment: see `internal/platform/config`. Secrets (`MASTER_KEY`,
`JWT_SIGNING_KEY`) come only from the environment; in dev they are generated
per run if missing.

Every module under `internal/` has a README with its endpoints, tables, jobs
and the sensitive data it touches. No README, no merge.

| Module | README |
|--------|--------|
| platform | `internal/platform/README.md` |
| identity | `internal/identity/README.md` |
| arca-gateway | `internal/arca/README.md` |
| portal (read views) | `internal/portal/README.md` |
| worker and job queue | `internal/worker/README.md` |
