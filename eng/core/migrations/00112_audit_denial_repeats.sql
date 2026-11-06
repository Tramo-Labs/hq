-- +goose Up
-- Coalesced authorization denials. audit_log stays append only: the first
-- 403 per (tenant, user, operation) in a window is an audit_log row as
-- always; the repeats in that window only bump this counter, keyed by that
-- row. One counter row per audit row, so growth is bounded to two rows per
-- key and window however hard a member hammers a forbidden operation.
create table audit_denial_repeats (
  audit_id   bigint primary key references audit_log(id),
  tenant_id  uuid not null references tenants(id) on delete cascade,
  count      integer not null check (count > 0),
  last_at    timestamptz not null
);

-- Finding the open window of a denial: the newest authz.denied row of the
-- user and operation in the tenant.
create index audit_log_denials on audit_log (tenant_id, user_id, subject, at desc)
  where action = 'authz.denied';

alter table audit_denial_repeats enable row level security;
grant select, insert, update on audit_denial_repeats to tramo_app;
create policy denial_repeats_tenant on audit_denial_repeats for all to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table audit_denial_repeats force row level security;

-- +goose Down
drop table audit_denial_repeats;
drop index audit_log_denials;
