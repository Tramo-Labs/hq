-- +goose Up
-- Row level security for the base tables, the restricted role every tenant
-- transaction runs as, per tenant data keys and the idempotency store.
--
-- How tenancy is enforced (ADR 0002): the pool connects as the owner of the
-- schema, and every request scoped to a tenant runs inside a transaction that
-- does SET LOCAL ROLE tramo_app and sets app.tenant_id and app.user_id.
-- tramo_app has no BYPASSRLS, so a query that forgets a WHERE tenant_id = ...
-- still only sees the current tenant.

-- +goose StatementBegin
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'tramo_app') then
    create role tramo_app nologin;
  end if;
end $$;
-- +goose StatementEnd

-- Membership in tramo_app lets the owner SET ROLE even when it is not a
-- superuser (managed Postgres).
-- +goose StatementBegin
do $$
begin
  execute format('grant tramo_app to %I', current_user);
end $$;
-- +goose StatementEnd

create function app_current_tenant() returns uuid language sql stable as $$
  select nullif(current_setting('app.tenant_id', true), '')::uuid
$$;
create function app_current_user() returns uuid language sql stable as $$
  select nullif(current_setting('app.user_id', true), '')::uuid
$$;

grant usage on schema public to tramo_app;
-- Tables created by later migrations (any module) are reachable by
-- tramo_app; each of them must still enable RLS with a tenant policy.
alter default privileges in schema public
  grant select, insert, update, delete on tables to tramo_app;
alter default privileges in schema public
  grant usage, select on sequences to tramo_app;

-- tenants: a tenant transaction sees only its own row.
alter table tenants enable row level security;
grant select, update on tenants to tramo_app;
create policy tenant_self on tenants to tramo_app
  using (id = app_current_tenant());

-- users: no tenant_id (a user can belong to several tenants). Visible when it
-- is the caller or a member of the current tenant. Secrets are not readable.
alter table users enable row level security;
grant select (id, email, created_at) on users to tramo_app;
create policy user_visible on users to tramo_app
  using (
    id = app_current_user()
    or exists (select 1 from memberships m
               where m.user_id = users.id and m.tenant_id = app_current_tenant())
  );

alter table memberships enable row level security;
grant select, insert, update, delete on memberships to tramo_app;
create policy membership_tenant on memberships to tramo_app
  using (tenant_id = app_current_tenant() or user_id = app_current_user())
  with check (tenant_id = app_current_tenant());

-- refresh_tokens are only touched by identity through the owner connection.
alter table refresh_tokens enable row level security;
alter table refresh_tokens add column tenant_id uuid references tenants(id) on delete cascade;

-- audit_log: append only (trigger in 00001), tenant scoped reads.
alter table audit_log enable row level security;
grant select, insert on audit_log to tramo_app;
create policy audit_read on audit_log for select to tramo_app
  using (tenant_id = app_current_tenant());
create policy audit_write on audit_log for insert to tramo_app
  with check (tenant_id = app_current_tenant());

-- The idempotency table of 00001 required a tenant, but registration creates
-- the tenant itself. It never held data; replaced by a scoped store.
drop table idempotency_keys;
create table idempotency_requests (
  scope         text not null,          -- tenant uuid, or anon:<route> before a tenant exists
  key           uuid not null,
  tenant_id     uuid references tenants(id) on delete cascade,
  request_hash  bytea not null,         -- sha256 of method, path and body
  status        int not null default 0, -- 0 while the first request is in flight
  content_type  text,
  response      bytea,
  created_at    timestamptz not null default now(),
  primary key (scope, key)
);
create index on idempotency_requests (created_at);
alter table idempotency_requests enable row level security;
create policy idempotency_tenant on idempotency_requests to tramo_app
  using (tenant_id = app_current_tenant());

-- One data key per tenant (AES-256-GCM), stored wrapped by the master key.
create table tenant_keys (
  tenant_id    uuid not null references tenants(id) on delete cascade,
  version      int not null,
  wrapped_key  bytea not null,
  created_at   timestamptz not null default now(),
  primary key (tenant_id, version)
);
alter table tenant_keys enable row level security;
grant select on tenant_keys to tramo_app;
create policy tenant_keys_tenant on tenant_keys to tramo_app
  using (tenant_id = app_current_tenant());

-- +goose Down
drop table tenant_keys;
drop table idempotency_requests;
create table idempotency_keys (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  key           uuid not null,
  request_hash  bytea not null,
  status        int not null,
  response      bytea not null,
  created_at    timestamptz not null default now(),
  primary key (tenant_id, key)
);
drop policy audit_write on audit_log; drop policy audit_read on audit_log;
alter table audit_log disable row level security;
alter table refresh_tokens drop column tenant_id;
alter table refresh_tokens disable row level security;
drop policy membership_tenant on memberships;
alter table memberships disable row level security;
drop policy user_visible on users;
alter table users disable row level security;
drop policy tenant_self on tenants;
alter table tenants disable row level security;
revoke all on all tables in schema public from tramo_app;
alter default privileges in schema public revoke all on tables from tramo_app;
alter default privileges in schema public revoke all on sequences from tramo_app;
revoke usage on schema public from tramo_app;
drop function app_current_user();
drop function app_current_tenant();
