-- +goose Up
-- Base of every module: tenants, memberships and the append-only audit log.
-- Business tables carry tenant_id and are protected by row level security;
-- each transaction sets app.tenant_id before touching them.
create extension if not exists citext;
create extension if not exists pgcrypto;

create table tenants (
  id          uuid primary key default gen_random_uuid(),
  slug        text not null unique,
  name        text not null,
  cuit        bigint unique,
  created_at  timestamptz not null default now()
);

create table users (
  id            uuid primary key default gen_random_uuid(),
  email         citext not null unique,
  password_hash text,                 -- argon2id; null when the user only uses magic links
  totp_secret   bytea,                -- encrypted with the tenant data key
  created_at    timestamptz not null default now()
);

create type role as enum ('owner', 'approver', 'viewer', 'operator', 'accountant');

create table memberships (
  user_id    uuid not null references users(id) on delete cascade,
  tenant_id  uuid not null references tenants(id) on delete cascade,
  role       role not null,
  primary key (user_id, tenant_id)
);

create table refresh_tokens (
  id          uuid primary key default gen_random_uuid(),
  user_id     uuid not null references users(id) on delete cascade,
  family_id   uuid not null,
  token_hash  bytea not null unique,   -- sha256 of the opaque token
  expires_at  timestamptz not null,
  used_at     timestamptz,
  revoked_at  timestamptz,
  created_at  timestamptz not null default now()
);
create index on refresh_tokens (family_id);

create table idempotency_keys (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  key           uuid not null,
  request_hash  bytea not null,
  status        int not null,
  response      bytea not null,
  created_at    timestamptz not null default now(),
  primary key (tenant_id, key)
);

create table audit_log (
  id          bigint generated always as identity primary key,
  tenant_id   uuid references tenants(id),
  user_id     uuid references users(id),
  action      text not null,
  subject     text not null,          -- table:id
  request_id  text,
  ip          inet,
  detail      jsonb,
  at          timestamptz not null default now()
);

-- +goose StatementBegin
create function audit_log_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'audit_log is append only';
end $$;
-- +goose StatementEnd
create trigger audit_log_no_update before update or delete on audit_log
  for each row execute function audit_log_immutable();

-- +goose Down
drop table audit_log; drop function audit_log_immutable();
drop table idempotency_keys; drop table refresh_tokens; drop table memberships;
drop type role; drop table users; drop table tenants;
