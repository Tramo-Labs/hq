-- +goose Up
-- Hardening of 00002 (ADR 0002, revised):
--   1. Both application roles are forced to NOSUPERUSER NOBYPASSRLS, also
--      when they already existed with other attributes.
--   2. db.System no longer runs as the schema owner: it switches to
--      tramo_system, a role that only reaches the platform and identity
--      tables listed here through explicit policies. A new business table
--      gives it nothing, so cross tenant access has to be granted table by
--      table in a migration that names its use.
--   3. FORCE ROW LEVEL SECURITY on every table, so a connection that owns
--      the tables but is not a superuser (managed Postgres, prod) sees no
--      rows unless it switches to one of the two roles.

-- +goose StatementBegin
do $$
begin
  if not exists (select 1 from pg_roles where rolname = 'tramo_system') then
    create role tramo_system nologin;
  end if;
  alter role tramo_app    nosuperuser nobypassrls nologin nocreaterole nocreatedb;
  alter role tramo_system nosuperuser nobypassrls nologin nocreaterole nocreatedb;
  execute format('grant tramo_system to %I', current_user);
end $$;
-- +goose StatementEnd

grant usage on schema public to tramo_system;
grant execute on function app_current_tenant(), app_current_user() to tramo_app, tramo_system;

-- What tramo_system may touch, and why:
--   tenants, users, memberships: registration, login, adding a member (the
--     user row has no tenant).
--   refresh_tokens: sessions are looked up by token hash before any tenant
--     is known.
--   idempotency_requests: keys are claimed before the handler opens its
--     tenant transaction, and registration has no tenant.
--   tenant_keys: a data key is created with the tenant.
--   audit_log: login failures and registration happen outside a tenant.
grant select, insert, update on tenants, users, memberships to tramo_system;
grant select, insert, update, delete on refresh_tokens, idempotency_requests to tramo_system;
grant select, insert on tenant_keys, audit_log to tramo_system;

create policy system_all on tenants              to tramo_system using (true) with check (true);
create policy system_all on users                to tramo_system using (true) with check (true);
create policy system_all on memberships          to tramo_system using (true) with check (true);
create policy system_all on refresh_tokens       to tramo_system using (true) with check (true);
create policy system_all on idempotency_requests to tramo_system using (true) with check (true);
create policy system_all on tenant_keys          to tramo_system using (true) with check (true);
create policy system_all on audit_log            to tramo_system using (true) with check (true);

alter table tenants              force row level security;
alter table users                force row level security;
alter table memberships          force row level security;
alter table refresh_tokens       force row level security;
alter table idempotency_requests force row level security;
alter table tenant_keys          force row level security;
alter table audit_log            force row level security;

-- +goose Down
alter table audit_log            no force row level security;
alter table tenant_keys          no force row level security;
alter table idempotency_requests no force row level security;
alter table refresh_tokens       no force row level security;
alter table memberships          no force row level security;
alter table users                no force row level security;
alter table tenants              no force row level security;
drop policy system_all on audit_log;
drop policy system_all on tenant_keys;
drop policy system_all on idempotency_requests;
drop policy system_all on refresh_tokens;
drop policy system_all on memberships;
drop policy system_all on users;
drop policy system_all on tenants;
revoke all on tenants, users, memberships, refresh_tokens, idempotency_requests, tenant_keys, audit_log from tramo_system;
revoke usage on schema public from tramo_system;
