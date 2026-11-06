-- +goose Up
-- One time login links (F10, D-17). Only a SHA-256 of the token is stored;
-- the token itself travels only in the email. Single use (used_at), 15
-- minute expiry. tenant_id is the tenant asked for, null for the default.
--
-- Like invites, only identity touches it, through tramo_system: links are
-- looked up by token hash before any tenant is known. tramo_app gets
-- nothing.
create table magic_links (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null references users(id) on delete cascade,
  tenant_id   uuid references tenants(id) on delete cascade,
  token_hash  bytea not null unique,
  expires_at  timestamptz not null,
  used_at     timestamptz,
  created_at  timestamptz not null default now()
);
create index magic_links_expires on magic_links (expires_at);

revoke all on magic_links from tramo_app;
grant select, insert, update, delete on magic_links to tramo_system;
alter table magic_links enable row level security;
create policy system_all on magic_links to tramo_system using (true) with check (true);
alter table magic_links force row level security;

-- +goose Down
drop table magic_links;
