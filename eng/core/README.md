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
