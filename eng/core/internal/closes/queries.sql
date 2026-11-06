-- Queries of the closes module (sqlc). Every one runs under the tenant
-- scope: RLS on closes.closes and closes.due limits it to the tenant of
-- the transaction, and the explicit tenant_id filters keep plans honest.

-- name: LockShared :exec
-- Taken by any write that asked whether a date is closed: a close waits
-- for it, so what checked "open" commits before the close counts.
select pg_advisory_xact_lock_shared(hashtextextended('closes:' || app_current_tenant()::text, 0));

-- name: LockExclusive :exec
-- Taken by a close for the rest of its transaction.
select pg_advisory_xact_lock(hashtextextended('closes:' || app_current_tenant()::text, 0));

-- name: MemberRole :one
-- The caller's role from memberships, not from the token.
select role::text from memberships where user_id = app_current_user() and tenant_id = app_current_tenant();

-- name: InsertClose :one
-- A period already closed inserts nothing (no rows): already_closed.
insert into closes.closes (tenant_id, cadence, period_start, period_end, closed_by, summary)
values (app_current_tenant(), $1, $2, $3, app_current_user(), $4)
on conflict (tenant_id, period_start) do nothing
returning id, cadence, period_start, period_end, closed_at, closed_by, summary;

-- name: IsClosed :one
select exists (select 1 from closes.closes where tenant_id = app_current_tenant() and period_start = $1);

-- name: GetClose :one
select id, cadence, period_start, period_end, closed_at, closed_by, summary
  from closes.closes where tenant_id = app_current_tenant() and id = $1;

-- name: ListCloses :many
-- Newest first. A cursor is the period_start of the last row seen.
select id, cadence, period_start, period_end, closed_at, closed_by, summary
  from closes.closes
 where tenant_id = app_current_tenant()
   and (sqlc.narg(before)::date is null or period_start < sqlc.narg(before)::date)
 order by period_start desc
 limit sqlc.arg(lim);

-- name: ClosedPeriodOf :one
-- The earliest closed period any of the dates falls in.
select cadence, period_start, period_end from closes.closes c
 where c.tenant_id = app_current_tenant()
   and exists (select 1 from unnest(sqlc.arg(dates)::date[]) d where d >= c.period_start and d < c.period_end)
 order by c.period_start
 limit 1;

-- name: MarkDue :execrows
-- The job's "para cerrar" mark; a period already marked is left as it is.
insert into closes.due (tenant_id, cadence, period_start, period_end)
values (app_current_tenant(), $1, $2, $3)
on conflict (tenant_id, period_start) do nothing;

-- name: PendingDue :many
-- Periods marked to close and not closed yet, oldest first.
select d.cadence, d.period_start, d.period_end, d.marked_at from closes.due d
 where d.tenant_id = app_current_tenant()
   and not exists (select 1 from closes.closes c where c.tenant_id = d.tenant_id and c.period_start = d.period_start)
 order by d.period_start;
