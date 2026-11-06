# worker (and platform/jobs)

Background jobs of core on River, a job queue in Postgres. Owned by backend.
`internal/platform/jobs` is the queue (pool, clients, insert, tenant scope);
`internal/worker` registers the job kinds and the schedule; `cmd/worker` runs
them. Decisions in ADR 0006.

## How a module adds a job

```go
type CloseMonthArgs struct {
    Tenant uuid.UUID `json:"tenant"`
    Period string    `json:"period"` // the natural key with the tenant
}
func (CloseMonthArgs) Kind() string { return "matching.close_month" }
func (CloseMonthArgs) InsertOpts() river.InsertOpts {
    return river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}
}

type CloseMonthWorker struct {
    river.WorkerDefaults[CloseMonthArgs]
    DB *db.DB
}
func (w *CloseMonthWorker) Work(ctx context.Context, job *river.Job[CloseMonthArgs]) error {
    ctx = jobs.TenantContext(ctx, job.Args.Tenant) // the only way a job gets a scope
    return w.DB.InTenant(ctx, func(tx pgx.Tx) error { ... })
}
```

Register it in `worker.New` (and, if periodic, add a `river.PeriodicJob`).
Enqueue from the api with `jobs.Insert(ctx, inserter, args)`: it returns
false when a job with the same key already exists, so nothing runs twice.

Rules: arguments carry the tenant and the natural key, never secrets or
personal data (they are stored in clear in `river.river_job`); `Work` must be
safe to retry; the key covers completed jobs too, so a finished month is not
redone by a second enqueue.

## Jobs today

| Kind | Key | Schedule | Does |
|------|-----|----------|------|
| `platform.maintenance` | day (Argentina) | hourly attempt, runs once a day | deletes idempotency keys older than 24 h, refresh tokens, invitations and magic links expired more than 7 days ago, and magic link requests older than a day (left by a failed enqueue) |
| `platform.month_close` | month closed (`YYYY-MM`, the month before now in Argentina) | hourly attempt, runs once a month | lists every tenant through `tramo_system` (`identity.TenantIDs`, ids only) and enqueues `matching.run_month` for each with `jobs.Insert`; those are unique by tenant and period, so a retry or a second fan out enqueues nothing (`TestMonthCloseEnqueuesEveryTenantOnce`, `TestMonthCloseClosesThePreviousArgentineMonth`; mutations checked: not unique, UTC instead of Argentina, a tenant skipped, the current month, each fails) |
| `identity.magic_link` | the pending request's id only (one job per request; email and IP stay in `magic_link_requests`, never in the arguments) | on request (`POST /v1/auth/magic-link`) | takes the request (deletes it), looks the email up and, if it has an account, stores a 15 minute link and mails it; otherwise nothing. Needs `MAIL_OUTBOX` and `APP_BASE_URL` in the worker (`TestMagicLinkJobSendsThroughTheQueue` reads `river.river_job` and finds no email or IP) |

## Tables

Schema `river` (`river_job`, `river_queue`, `river_leader`, `river_client`,
`river_client_queue`, `river_migration`), created by goose migrations 00005
and 00006, which call River's own migrator pinned to River versions 4 and 8.
Migration 00007: role `tramo_jobs` (no superuser, no bypass, no login) is the
only role with access, with forced RLS and a single policy for it.

## Roles and connections

- The queue uses its own pool; every connection does `SET ROLE tramo_jobs`.
  That pool cannot read any business or identity table (tested).
- A job's business work uses the normal `db.InTenant` with
  `jobs.TenantContext(ctx, tenant)`: RLS of that tenant, nothing else
  (tested).
- In production `tramo_runtime` is a member of `tramo_jobs` (00007 grants it
  when the role exists), so the worker and the api need no extra secret.

## Sensitive data

Job arguments are stored in clear: tenant id and natural key only. No
tokens, keys, amounts or personal data in arguments; load them inside the
job under the tenant scope.

## How it is tested

`internal/worker/worker_test.go` on an embedded Postgres with River running:

- the same key inserted twice, then twenty concurrent inserts, then again
  after the job completed: one job, one run; another key runs again (two
  rows stored). Mutation: without `UniqueOpts` the second insert is created
  and the test fails.
- the maintenance job deletes the expired idempotency key and refresh token
  and keeps the live ones.
- the jobs pool is denied on `tenants`, `users`, `audit_log` and
  `ledger.movements`, and reads its own queue.
- a tenant job sees exactly its tenant under RLS.
- the day key uses Argentina time (01:30 UTC is the previous day).

Plus `internal/platform/db`: `tramo_jobs` has no superuser, bypass or login,
also when it pre-existed with them; every river table has forced RLS.

Not run: the worker binary against a real deployment (no Docker here); the
periodic schedule firing in time (tested by its key function, not by waiting
an hour).
