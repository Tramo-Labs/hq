-- +goose Up
-- Pending memberships (F26, D-19). Adding a person to a tenant is always an
-- invitation: the row waits here until the person accepts the mailed link,
-- and only then becomes a row of memberships. A separate table on purpose:
-- everything that grants access, lists a switcher or counts owners reads
-- memberships, so a pending row gives nothing by construction.
--
-- email is copied at invitation time (lowercased): the inviting tenant lists
-- its pending people, and tramo_app cannot see a user row before the person
-- belongs to the tenant.
create table pending_memberships (
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  email       text not null,
  role        role not null,
  invited_by  uuid references users(id) on delete set null,
  created_at  timestamptz not null default now(),
  primary key (tenant_id, user_id)
);

alter table pending_memberships enable row level security;
grant select, insert, update, delete on pending_memberships to tramo_app;
create policy pending_tenant on pending_memberships to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
grant select, insert, update, delete on pending_memberships to tramo_system;
create policy system_all on pending_memberships to tramo_system using (true) with check (true);
alter table pending_memberships force row level security;

-- +goose Down
drop table pending_memberships;
