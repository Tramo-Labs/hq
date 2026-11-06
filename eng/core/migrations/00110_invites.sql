-- +goose Up
-- Invitations for people added to a tenant before they have a password
-- (F8). Only a SHA-256 of the token is stored; the token itself travels
-- only in the email. Single use (used_at), 72 hour expiry, and a new
-- invitation revokes the person's earlier unused ones.
--
-- Like refresh_tokens, only identity touches it, through tramo_system:
-- invites are looked up by token hash before any tenant is known.
-- tramo_app gets nothing (the default privileges of 00002 are revoked).
create table invites (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  created_by  uuid references users(id) on delete set null,
  token_hash  bytea not null unique,
  expires_at  timestamptz not null,
  used_at     timestamptz,
  revoked_at  timestamptz,
  created_at  timestamptz not null default now()
);
create index invites_user on invites (user_id) where used_at is null and revoked_at is null;

revoke all on invites from tramo_app;
grant select, insert, update, delete on invites to tramo_system;
alter table invites enable row level security;
create policy system_all on invites to tramo_system using (true) with check (true);
alter table invites force row level security;

-- +goose Down
drop table invites;
