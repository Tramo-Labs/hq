# listrange

Reads the date filter of a list, as `api/openapi.yaml` writes it for
`GET /v1/matching/pending`, `GET /v1/matching/matches` and
`GET /v1/documents/invoices`: a month (`period`, YYYY-MM) or a range of
civil dates (`from` and `to`, both included, as in `/v1/exports/{kind}`).
Owned by datos; matching and documents use it.

`Parse` turns either into one `Range` (first and last day, midnight UTC),
so each list filters by one kind of range and a month filters exactly as
before. The rules the spec cannot express in its schema are `ErrInvalid`,
which the handlers answer with 400: `from` and `to` go together, neither
goes with `period`, and `to` is not before `from`. A time of day or a zone
on a date does not move it.

Which date each list filters by is the list's: the movement's date for
pending movements and for matches (a match is listed when any of its
movements falls in the range), the day an invoice was stored (Argentina
time) for invoices.

No tables, endpoints, jobs or sensitive data of its own.

## How it is tested

`go test ./internal/listrange/`: months (leap February included), a
fortnight, one day, every refused pairing, and civil dates whatever the
zone. `TestListsFilterByDateRange` (internal/app) runs the three routes
against the real API: each filters by from and to, by period as before,
and answers 400 to each pairing rule. Mutation checked: ignoring from and
to, or accepting to before from, fails it on every route.
