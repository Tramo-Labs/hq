# core

Backend of the product: REST API (OpenAPI first), background worker and the
ARCA gateway. Design and rules in `../ARQUITECTURA.md`.

```
make build      # compiles the three binaries
make test       # go test -race
make up         # docker compose: postgres, api, worker, arca-gateway, mailpit
```

Swagger UI: http://localhost:8080/docs/ once the api is up.

Every module under `internal/` has a README with its endpoints, tables, jobs
and the sensitive data it touches. No README, no merge.
