# 0008: Period close: append only closes, a per tenant lock, cadence as a default

Date: 2026-11-06. Status: accepted.

Context: spec §6 and D-6: an operator closes a tenant's period once its
Suggested tray is empty, and the close freezes it. The plan (lead-2,
lead-4) splits the work: backend owns the table, the endpoints, the freeze
check and the fortnightly mark; datos owns matching's numbers per range and
making matching and ingestion respect the freeze. The cadence is fortnight
by default; Martín has not chosen between calendar fortnight and 14 days.

Decisions:

1. **Closes are append only.** `closes.closes` grants `tramo_app` select
   and insert, never update or delete. Reopening is not built; when Martín
   decides who reopens, it is a new migration and a new, audited
   operation, not an edit of the row.
2. **A per tenant advisory lock orders closes and writes.** The close
   checks the pending Suggested count and inserts in one transaction, but
   under read committed a run could add a Suggested item after the count
   and before the commit. Writes that must respect the freeze ask
   `closes.ClosedOn` in their own transaction, which takes the shared side
   of `pg_advisory_xact_lock_shared(hash('closes:' || tenant))`; the close
   takes the exclusive side before counting. A write that saw "open"
   therefore commits before the close counts, and is counted. Considered
   and rejected: serializable isolation for every matching write (retries
   everywhere, for one rare event), and a trigger on matching's tables
   (backend writing into datos's schema). The lock is per tenant, not per
   period: closes are rare, and per period would need the lock key to
   follow the cadence.
3. **`closes` does not import matching.** It defines `Numbers` (pending
   Suggested and summary per range) and `ClosedOn` with the shape of
   matching's hook; `app` wires them. Until matching provides `Numbers`,
   closing answers 501, after the role check.
4. **The cadence is a default in one place** (`DefaultCadence`). The table
   keeps the cadence of each close. With a single cadence, unique (tenant,
   start) also prevents overlaps; a second cadence needs an exclusion
   constraint first.
5. **The fortnightly job marks, never closes**, under each tenant's own
   scope (`closes.due`, RLS), so it needs no new `tramo_system` access; the
   mark is unique by tenant and period, which makes the job idempotent
   without a durable fan out mark like `platform.month_closes`.

Consequences: a write that asks `ClosedOn` waits while a close of the same
tenant runs (milliseconds); a close waits for in flight writes of that
tenant. Matching's month based hook must move to dates (datos-5) before the
freeze is exact for fortnights.
