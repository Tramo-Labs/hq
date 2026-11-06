-- +goose Up
-- Durable mark of each month the monthly fan out closed (platform job
-- platform.month_close). River forgets completed jobs after 24 hours, so
-- its unique key cannot stop the hourly schedule from closing the same
-- month again the next day; this mark does. It is written in the same
-- transaction as the fan out and only commits if every tenant's month run
-- was enqueued. Not tenant data: one row per period, platform's own.
create schema platform;
grant usage on schema platform to tramo_system;

create table platform.month_closes (
  period     text primary key check (period ~ '^\d{4}-(0[1-9]|1[0-2])$'),
  closed_at  timestamptz not null default now(),
  tenants    integer not null
);

grant select, insert on platform.month_closes to tramo_system;
alter table platform.month_closes enable row level security;
create policy system_all on platform.month_closes to tramo_system using (true) with check (true);
alter table platform.month_closes force row level security;

-- +goose Down
drop table platform.month_closes;
drop schema platform;
