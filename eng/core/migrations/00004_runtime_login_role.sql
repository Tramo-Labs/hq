-- +goose Up
-- Production split (ADR 0005): migrations run once with a privileged URL
-- (MIGRATE_DATABASE_URL), the api runs with DATABASE_URL as tramo_runtime,
-- a LOGIN role that owns nothing, is not superuser, cannot bypass RLS, and
-- can only SET ROLE to tramo_app (tenant transactions) or tramo_system
-- (the allowlisted cross tenant path). A query on the raw pool reaches no
-- table: tramo_runtime has no privileges of its own.
--
-- tramo_runtime is created by the operator, because it has a password (see
-- deploy/postgres-init/10-runtime-role.sh). This migration only wires it
-- when it exists; in dev and tests nothing changes.

-- +goose StatementBegin
do $$
begin
  if exists (select 1 from pg_roles where rolname = 'tramo_runtime') then
    alter role tramo_runtime nosuperuser nobypassrls nocreaterole nocreatedb noinherit;
    grant tramo_app, tramo_system to tramo_runtime;
    execute format('grant connect on database %I to tramo_runtime', current_database());
  end if;
end $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
do $$
begin
  if exists (select 1 from pg_roles where rolname = 'tramo_runtime') then
    revoke tramo_app, tramo_system from tramo_runtime;
  end if;
end $$;
-- +goose StatementEnd
