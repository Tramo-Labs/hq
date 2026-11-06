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
make up          # docker compose: postgres, api, worker, arca-gateway, mailpit
```

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
