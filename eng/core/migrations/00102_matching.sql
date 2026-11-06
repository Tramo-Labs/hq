-- +goose Up
-- Matching: the items movements can settle, each run's matches and pending
-- movements, and the append only record of human decisions.

create schema matching;
grant usage on schema matching to tramo_app;

create table matching.items (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  source      text not null,              -- erp, document, manual
  source_id   text not null,              -- id in the source, for idempotent loads
  kind        text not null,
  date        date not null,
  due         date,
  amount      bigint not null,            -- cents, with the sign it will have in the bank
  refs        text[] not null default '{}',
  confidence  real not null,
  party       text not null default '',
  created_at  timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, source, source_id)
);

create table matching.runs (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  period      text not null,              -- YYYY-MM
  config      jsonb not null,
  started_at  timestamptz not null default now(),
  unique (tenant_id, id)
);

-- A match ID is a hash of rule, version and links: the same match found by
-- a second run is the same row.
create table matching.matches (
  tenant_id     uuid not null references tenants(id) on delete cascade,
  id            text not null,
  run_id        uuid not null,
  rule_id       text not null,
  rule_version  int not null,
  tray          text not null check (tray in ('auto', 'suggested')),
  movement_ids  uuid[] not null,
  item_ids      uuid[] not null,
  adjustments   jsonb not null default '[]',
  evidence      jsonb not null,
  created_at    timestamptz not null default now(),
  primary key (tenant_id, id),
  foreign key (tenant_id, run_id) references matching.runs (tenant_id, id)
);

create table matching.pending (
  tenant_id    uuid not null references tenants(id) on delete cascade,
  run_id       uuid not null,
  movement_id  uuid not null,
  tray         text not null check (tray in ('suggested', 'exception')),
  reason       text not null,
  candidates   jsonb not null default '[]',
  primary key (tenant_id, run_id, movement_id),
  foreign key (tenant_id, run_id) references matching.runs (tenant_id, id)
);

create table matching.decisions (
  id          bigint generated always as identity primary key,
  tenant_id   uuid not null references tenants(id) on delete cascade,
  key         uuid not null,              -- client's idempotency key
  match_id    text not null,
  action      text not null check (action in ('approve', 'reject')),
  user_id     uuid not null references users(id),
  role        text not null,
  at          timestamptz not null default now(),
  note        text not null default '',
  snapshot    jsonb not null,             -- the match as it was decided on
  unique (tenant_id, key),
  unique (tenant_id, match_id),
  foreign key (tenant_id, match_id) references matching.matches (tenant_id, id)
);

-- +goose StatementBegin
create function matching.decisions_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'matching.decisions is append only';
end $$;
-- +goose StatementEnd
create trigger decisions_no_update before update or delete on matching.decisions
  for each row execute function matching.decisions_immutable();

grant select, insert, update, delete on matching.items, matching.runs, matching.matches, matching.pending to tramo_app;
grant select, insert on matching.decisions to tramo_app;

alter table matching.items     enable row level security;
alter table matching.items     force row level security;
alter table matching.runs      enable row level security;
alter table matching.runs      force row level security;
alter table matching.matches   enable row level security;
alter table matching.matches   force row level security;
alter table matching.pending   enable row level security;
alter table matching.pending   force row level security;
alter table matching.decisions enable row level security;
alter table matching.decisions force row level security;

create policy items_tenant on matching.items to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy runs_tenant on matching.runs to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy matches_tenant on matching.matches to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy pending_tenant on matching.pending to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy decisions_tenant on matching.decisions to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());

-- +goose Down
drop schema matching cascade;
