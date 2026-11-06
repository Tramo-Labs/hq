# devseed

Fills a local development database with synthetic data for `make dev`: two
tenants with synthetic CUITs, one user per role (the accountant and the
operator over both tenants, plus an owner for the second tenant so
isolation can be tried by hand), a Bancor statement ingested with ledger's
own ingester and a matching run with matching's own engine, so every tray
has items, and two invoices left for linking by hand. Owned by backend;
called only from `cmd/dev`.

**Dev only.** `Guard` refuses any environment but dev (`APP_ENV` and the
ARCA environment), so it can never run against production.

It is idempotent: running it again changes nothing.

## Secrets

Generated once per data directory and kept in `dev-secrets.json` there
(mode 0600, never in the repo): the master key (tenant data keys stay
readable across restarts), the JWT seed (sessions survive a restart) and
the users' passwords, which `make dev` prints. Argon2 runs with cheap
parameters: dev only.

## Endpoints, jobs

None. It writes through the modules' own stores. It is allowlisted to open
`tramo_system` transactions to seed tenants, users and memberships
(`internal/platform/db/allowlist_test.go`), because no tenant scope exists
yet at that point.

## Tables

None of its own; it writes into identity, ledger, documents and matching
tables.

## Sensitive data

Synthetic only: made up names, emails under test domains, CUITs with valid
check digits that belong to no one, and ledger's test fixture.

## How it is tested

`go test ./internal/devseed/` (embedded Postgres): the guard refuses
anything but dev; secrets are stable across runs and private (0600); the
seed is idempotent and fills every tray; the seeded items follow matching's
contract; a seed error is fatal, not skipped; seeded users log in with the
printed passwords.
