# closes

The period close of a tenant (spec §6, guide §2.4, D-6). An operator
freezes a period once its Suggested tray is empty; the close keeps the
summary the client sees in the portal and the console. Owned by backend.
Matching's numbers for a period (pending Suggested, the summary) and
respecting the freeze inside matching and ingestion are datos's, through
the interface below.

**Status.** Table, endpoints, the freeze check and the fortnightly mark are
built. Closing answers 501 until matching provides `Numbers` (datos-5);
the role is checked first, so a non operator gets 403 either way.
Reopening is not built (Martín). No mail goes out until a provider is
chosen (Martín): the summary lives in the portal and the console.

## Periods

A cadence cuts a tenant's time into periods. The only one today, and the
default, is the calendar **fortnight**: the 1st to the 15th and the 16th to
the month's last day, so a month is always two whole periods. It is a
default (D-6, lead-2), not a decision: it is set in one place,
`DefaultCadence` in `period.go`, which the service, the job and the API
all read. A second cadence (for example 14 running days) is a new case in
`PeriodOf` and `PeriodStarting`, a new value in the `cadence` check of
migration 00200 and in the spec's enum, and, before two cadences coexist,
an exclusion constraint so closes cannot overlap.

A period is a half open range of civil dates `[start, end)`; the API shows
the first and the last day included. A period is over when its last day is
over in Argentina's time (UTC-3), not in UTC.

## Endpoints (api/openapi.yaml, tag `closes`)

| Method and path | Auth | Idempotency-Key | What it does |
|---|---|---|---|
| `GET /v1/closes` | bearer, any role | | The tenant's closes, newest first, pages of 50 (`cursor` from `nextCursor`), each with its frozen summary. |
| `POST /v1/closes` | bearer, operator | required | Closes the period starting on `periodStart`. 403 for any other role; 400 `invalid_period`; 409 `suggested_pending` (with `pendingSuggested`), `already_closed`, `period_not_over`; 501 while matching does not provide the numbers. Nothing is written on a refusal. |
| `GET /v1/closes/{closeId}` | bearer, any role | | One close with its summary; 404 for another tenant's. |

The role is read from `memberships` inside the transaction, not from the
token. The summary reuses the results panel's `Metric`: each number with
its source and its list; its `period` is the range (`2026-11-01/2026-11-15`).
`GET /v1/tenants` (console) carries `toClose`: the periods marked by the
job and not closed yet.

Related spec fields this module defines: `IngestResult.closedPeriod` and
`UploadedFile.closedPeriod` (a `PeriodRange`, from and to, both included),
set with the quarantine reason `period_closed` from `ClosedPeriodOf`, so
the portal can say "del 1 al 15 de octubre". Ingestion fills them (datos).

## The freeze, and the interface with matching

Everything is in this package; `closes` does not import matching, `app`
wires them.

```go
// Asked by any write that must not touch a closed period, in its own transaction.
func ClosedOn(ctx context.Context, tx pgx.Tx, dates []time.Time) (bool, error)
func ClosedPeriodOf(ctx context.Context, tx pgx.Tx, dates []time.Time) (Period, bool, error)

// Provided by matching (datos), for a range [from, to).
type Numbers interface {
    PendingSuggested(ctx context.Context, tx pgx.Tx, from, to time.Time) (int, error)
    Summary(ctx context.Context, tx pgx.Tx, from, to, now time.Time) ([]Metric, error)
}
```

`ClosedOn` and `ClosedPeriodOf` take the shared side of a per tenant
advisory lock until the caller's transaction ends; a close takes the
exclusive side before it counts. So a write that saw a period open commits
before the close counts what is pending, and the close then refuses with
what it added; no write slips between the count and the close
(`TestACloseWaitsForAWriteThatSawItOpen`, ADR 0008). `ClosedOn` has the
shape matching's `ClosedFunc` takes once it asks by dates instead of months
(datos-5). In SQL, `closes.closed_on(date)` (security invoker, the caller's
RLS) answers the same without the lock, for reads.

## Tables (migration 00200)

| Table | Holds | Access |
|---|---|---|
| `closes.closes` | one row per closed period: cadence, start, end (exclusive), when, the operator, the summary (jsonb). Unique by tenant and start. | `tramo_app` select and insert only (append only: no reopening), RLS by tenant, forced |
| `closes.due` | the job's "para cerrar" mark per tenant and period | `tramo_app` select and insert, RLS by tenant, forced |

## Jobs

`closes.mark_due` (internal/worker): hourly attempt, once per period over;
marks it for every tenant, never closes. See internal/worker/README.md.

## Sensitive data

None beyond what matching already holds: the summary is counts, amounts and
ages, no personal data. `closed_by` is a user id. Every close is audited
(`closes.close`, with the range and how many metrics).

## How it is tested

`go test ./internal/closes/` (embedded Postgres): fortnight boundaries
(months of 28 to 31 days, leap year, year end, the 15/16 border and the
month end by Argentina's clock); only an operator of the tenant closes;
`invalid_period`, `period_not_over`, `suggested_pending` with the count,
`already_closed`, each writing nothing; the pending count is asked for
exactly `[start, end)`; the summary is frozen as given; isolation between
tenants; pages of the list; `ClosedOn` at each border and per tenant, and
`closes.closed_on` agreeing with it; the lock; the job's mark once per
period and gone from pending once closed. `internal/app/closes_test.go`
covers the HTTP contract: 403 for owner, approver, viewer and accountant
with and without numbers, the 409 body, `Idempotency-Key` required and
replayed, the console's `toClose`.

Mutations checked: the shared lock removed fails the lock test; letting
every role but viewer close fails the role test (six refusals); cutting the
fortnight at the 14th fails the period tests.
