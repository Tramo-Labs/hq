# closes

The period close of a tenant (spec §6, guide §2.4, D-6). An operator
freezes a period once its Suggested tray is empty; the close keeps the
summary the client sees in the portal and the console. Owned by backend;
the numbers and the freeze in matching and ledger writes are datos's.

**Status.** Contract and periods only: the operations answer 501 until the
table and the store land (next commit). Reopening is not built (Martín).
The summary stays in the portal and the console: no mail goes out until a
provider is chosen (Martín).

## Periods

A cadence cuts a tenant's time into periods; it is a parameter. The only
one today, and the default (D-6, lead-2), is the calendar **fortnight**:
the 1st to the 15th and the 16th to the month's last day, so a month is
always two whole periods. A period is a half open range of civil dates
`[start, end)`; the API shows the last day included. A period is over
when its last day is over in Argentina's time (UTC-3), not in UTC.

## Endpoints (api/openapi.yaml, tag `closes`)

| Method and path | Auth | Idempotency-Key | What it does |
|---|---|---|---|
| `GET /v1/closes` | bearer, any role | | The tenant's closes, newest first, each with its frozen summary. |
| `POST /v1/closes` | bearer, operator | required | Closes the period starting on `periodStart`. 409 `suggested_pending` (with `pendingSuggested`), `already_closed`, `period_not_over`; 400 `invalid_period`. |
| `GET /v1/closes/{closeId}` | bearer, any role | | One close with its summary. |

The summary reuses the results panel's `Metric`: each number with its
source and the list it comes from, as the spec asks.

## Tables

None yet (next commit, migration range 00200-00249).

## Jobs

None yet: a fortnightly fan out will mark the tenants with a period to
close.

## Sensitive data

None beyond what matching already holds: the summary is counts, amounts
and ages, no personal data.

## How it is tested

`go test ./internal/closes/`: fortnight boundaries (months of 28 to 31
days, leap year), only a period's first day names it, an unknown cadence
is refused, and a period is over by Argentina's clock (01:00 UTC on the
16th is still the 15th there). Mutation checked: cutting the fortnight at
the 14th fails it.
