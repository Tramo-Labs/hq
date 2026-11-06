-- +goose Up
-- Period closes (spec §6, guide §2.4, D-6). A close freezes one period of a
-- tenant, cut by a cadence (calendar fortnight by default), and keeps the
-- summary as it was when it closed. Append only: reopening is not built,
-- so tramo_app may insert and read, never update or delete.
--
-- period_end is exclusive: a period is [period_start, period_end). With a
-- single cadence periods are aligned, so unique (tenant_id, period_start)
-- also keeps two closes from overlapping; a second cadence must add an
-- exclusion constraint first.
create schema closes;
grant usage on schema closes to tramo_app;

create table closes.closes (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  cadence       text not null check (cadence in ('fortnight')),
  period_start  date not null,
  period_end    date not null check (period_end > period_start),
  closed_at     timestamptz not null default now(),
  closed_by     uuid not null references users(id),
  summary       jsonb not null,
  unique (tenant_id, period_start)
);

grant select, insert on closes.closes to tramo_app;
alter table closes.closes enable row level security;
create policy closes_tenant on closes.closes to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table closes.closes force row level security;

-- The fortnightly job's mark: a period of the tenant is over ("para
-- cerrar"). The job never closes; an operator does. The mark stays after
-- the close; a period is pending while it has a mark and no close.
create table closes.due (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  cadence       text not null check (cadence in ('fortnight')),
  period_start  date not null,
  period_end    date not null check (period_end > period_start),
  marked_at     timestamptz not null default now(),
  primary key (tenant_id, period_start)
);

grant select, insert on closes.due to tramo_app;
alter table closes.due enable row level security;
create policy due_tenant on closes.due to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table closes.due force row level security;

-- Whether the civil date d falls in a closed period of the tenant in
-- scope. Security invoker: the caller's RLS applies, so it only ever sees
-- its own tenant. It takes no lock; a write that must not race a close
-- uses closes.ClosedOn in Go, which does.
create function closes.closed_on(d date) returns boolean
  language sql stable security invoker
  set search_path = pg_catalog, public
as $$
  select exists (
    select 1 from closes.closes c
     where c.tenant_id = app_current_tenant()
       and d >= c.period_start and d < c.period_end)
$$;
revoke all on function closes.closed_on(date) from public;
grant execute on function closes.closed_on(date) to tramo_app;

-- +goose Down
drop function closes.closed_on(date);
drop table closes.due;
drop table closes.closes;
drop schema closes;
