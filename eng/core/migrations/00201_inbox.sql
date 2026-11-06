-- +goose Up
-- The client's statement mailbox (D-22): an address per tenant, the senders
-- the owner authorized, and a receipt per message. The body, the subject
-- and the headers are never stored.
create schema inbox;
grant usage on schema inbox to tramo_app;

-- One active address per tenant; rotating revokes the current one. code is
-- the address' local part: 26 base32 characters (130 random bits).
create table inbox.addresses (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  code        text not null unique check (code ~ '^[a-z2-7]{26}$'),
  created_by  uuid references users(id) on delete set null,
  created_at  timestamptz not null default now(),
  revoked_at  timestamptz
);
create unique index addresses_one_active on inbox.addresses (tenant_id) where revoked_at is null;

grant select, insert, update on inbox.addresses to tramo_app;
alter table inbox.addresses enable row level security;
create policy addresses_tenant on inbox.addresses to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table inbox.addresses force row level security;

create table inbox.senders (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  email       text not null check (email = lower(email)),
  added_by    uuid references users(id) on delete set null,
  added_at    timestamptz not null default now(),
  removed_at  timestamptz
);
create unique index senders_one_active on inbox.senders (tenant_id, email) where removed_at is null;

grant select, insert, update on inbox.senders to tramo_app;
alter table inbox.senders enable row level security;
create policy senders_tenant on inbox.senders to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table inbox.senders force row level security;

-- message_id_hash: SHA-256 of the Message-ID, so a provider's retry of the
-- same message returns its receipt instead of ingesting twice. day is the
-- Argentine day it arrived, for the daily limits.
create table inbox.receipts (
  id               uuid primary key default gen_random_uuid(),
  tenant_id        uuid not null references tenants(id) on delete cascade,
  message_id_hash  bytea not null,
  received_at      timestamptz not null,
  day              date not null,
  sender           text not null,
  status           text not null check (status in ('accepted', 'refused')),
  reason           text,
  attachments      jsonb not null default '[]',
  check ((status = 'refused') = (reason is not null)),
  unique (tenant_id, message_id_hash)
);
create index receipts_day on inbox.receipts (tenant_id, day, status);
create index receipts_recent on inbox.receipts (tenant_id, received_at desc);

grant select, insert on inbox.receipts to tramo_app;
alter table inbox.receipts enable row level security;
create policy receipts_tenant on inbox.receipts to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table inbox.receipts force row level security;

-- Finding the tenant of an address happens before any tenant scope exists.
-- tramo_inbox exists for that alone: no login, no table privileges, only
-- EXECUTE on this function, which returns the tenant of an active address
-- and nothing else. Security definer, owned by the migration owner, with a
-- fixed search_path; the owner reads inbox.addresses through its own policy.
-- +goose StatementBegin
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'tramo_inbox') then
    create role tramo_inbox nologin;
  end if;
  alter role tramo_inbox nosuperuser nobypassrls nologin nocreaterole nocreatedb;
  execute format('grant tramo_inbox to %I', current_user);
  if exists (select 1 from pg_roles where rolname = 'tramo_runtime') then
    grant tramo_inbox to tramo_runtime;
  end if;
  execute format('create policy owner_lookup on inbox.addresses for select to %I using (true)', current_user);
end $$;
-- +goose StatementEnd

create function inbox.address_tenant(c text) returns uuid
  language sql stable security definer
  set search_path = pg_catalog, pg_temp
as $$
  select a.tenant_id from inbox.addresses a where a.code = lower(c) and a.revoked_at is null
$$;
revoke all on function inbox.address_tenant(text) from public;
grant usage on schema inbox to tramo_inbox;
grant execute on function inbox.address_tenant(text) to tramo_inbox;

-- +goose Down
drop function inbox.address_tenant(text);
drop table inbox.receipts;
drop table inbox.senders;
drop table inbox.addresses;
drop schema inbox;
-- tramo_inbox stays: roles are cluster wide and other databases may use it.
