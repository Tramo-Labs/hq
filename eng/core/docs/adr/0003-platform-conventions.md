# 0003: Platform conventions (spec, errors, idempotency, encryption, tests)

Date: 2026-11-06. Status: accepted.

1. **OpenAPI 3.0.3, not 3.1.** oapi-codegen does not support 3.1. The spec
   is the contract; `gen/api` is generated with chi and strict (typed)
   handlers, and every request is validated against the embedded spec before
   it reaches a handler. Generated code is committed so `go build` needs no
   tools; CI runs `make gen-check` to catch a stale `gen/`. Tool versions are
   pinned in the Makefile and installed into `./bin`.
2. **Errors are RFC 9457 problem+json** with a stable `code`. Details never
   echo submitted values. A spec test checks every 4xx and 5xx uses `Problem`.
3. **Idempotency.** Keys live in `idempotency_requests`, scoped by tenant, or
   by `anon:<path>` before a tenant exists (registration). The 00001 table
   required a tenant and never held data, so 00002 replaced it. The key is
   claimed with a single `insert ... on conflict do update where expired`, so
   two concurrent requests cannot both run. A 5xx or panic releases the key; a
   4xx is stored. Stored responses are in clear, so endpoints behind a key
   never return secrets: registration returns ids, and login and refresh
   (which return tokens) do not create resources and take no key. A spec test
   fails if a POST returning 201 lacks the header.
4. **Envelope encryption.** One AES-256-GCM data key per tenant in
   `tenant_keys`, wrapped by `MASTER_KEY` with the tenant id as additional
   data. Blobs carry format and key version in an authenticated header and
   bind tenant and purpose as additional data. Rotation adds a version; old
   blobs keep decrypting.
5. **Tests use an embedded Postgres 17** (`fergusstrange/embedded-postgres`),
   one server per test binary, a migrated template database cloned per test.
   No Docker needed; ports 54329 to 54338 for backend packages (54339 and up
   belong to other teams' runs).
6. **sqlc for module queries.** Platform keeps its handful of statements
   inline in pgx (they are generic infrastructure: role, settings,
   idempotency, keys). Business modules write SQL in `queries.sql` and use
   the sqlc output; nothing hand written maps rows.
