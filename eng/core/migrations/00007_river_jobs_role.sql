-- +goose Up
-- Access to River's queue (schema river, created by migrations 00005 and
-- 00006). ADR 0006.
--
-- tramo_jobs is the only role that touches the queue: NOLOGIN, no
-- superuser, no RLS bypass, no access to any business or identity table.
-- The worker's River client and the enqueue path both SET ROLE to it.
-- Business work inside a job still goes through db.InTenant with the
-- tenant carried in the job's arguments.

-- +goose StatementBegin
do $$
declare t record;
begin
  if not exists (select 1 from pg_roles where rolname = 'tramo_jobs') then
    create role tramo_jobs nologin;
  end if;
  alter role tramo_jobs nosuperuser nobypassrls nologin nocreaterole nocreatedb;
  execute format('grant tramo_jobs to %I', current_user);
  if exists (select 1 from pg_roles where rolname = 'tramo_runtime') then
    grant tramo_jobs to tramo_runtime;
  end if;

  grant usage on schema river to tramo_jobs;
  grant select, insert, update, delete on all tables in schema river to tramo_jobs;
  grant usage, select on all sequences in schema river to tramo_jobs;
  grant execute on all functions in schema river to tramo_jobs;

  -- Forced RLS like every other table; the queue is not tenant data, so
  -- the one policy lets tramo_jobs (and only it) see every row.
  for t in select tablename from pg_tables where schemaname = 'river' loop
    execute format('alter table river.%I enable row level security', t.tablename);
    execute format('alter table river.%I force row level security', t.tablename);
    execute format('create policy jobs_all on river.%I to tramo_jobs using (true) with check (true)', t.tablename);
  end loop;
end $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
do $$
declare t record;
begin
  for t in select tablename from pg_tables where schemaname = 'river' loop
    execute format('drop policy if exists jobs_all on river.%I', t.tablename);
  end loop;
  revoke all on all tables in schema river from tramo_jobs;
  revoke usage on schema river from tramo_jobs;
end $$;
-- +goose StatementEnd
