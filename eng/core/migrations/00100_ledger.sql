-- +goose Up
-- Ledger: accounts, uploaded files (accepted or quarantined), statements and
-- movements. Amounts are integer cents. Every table carries tenant_id and is
-- protected by row level security, forced also for the owner.

create schema ledger;
grant usage on schema ledger to tramo_app;

create table ledger.accounts (
  id          uuid primary key default gen_random_uuid(),
  tenant_id   uuid not null references tenants(id) on delete cascade,
  bank        text not null,          -- galicia, santander, bancor, macro, mercadopago
  number      text,                   -- as the client registered it; checked against the file when stated
  currency    text not null default 'ARS',
  name        text not null,
  created_at  timestamptz not null default now(),
  unique (tenant_id, id)
);

create table ledger.files (
  id           uuid primary key default gen_random_uuid(),
  tenant_id    uuid not null references tenants(id) on delete cascade,
  account_id   uuid not null,
  sha256       bytea not null,
  filename     text,
  status       text not null check (status in ('accepted', 'quarantined')),
  reason       text,                  -- reason code when quarantined
  detail       text,                  -- line, column, fingerprint; never cell contents
  fingerprint  text,
  layout_id    text,
  storage_key  text,                  -- encrypted object in the bucket
  received_at  timestamptz not null default now(),
  foreign key (tenant_id, account_id) references ledger.accounts (tenant_id, id),
  check ((status = 'quarantined') = (reason is not null)),
  unique (tenant_id, id)
);
-- The same file enters once per tenant. Quarantined copies can repeat: once
-- the layout is fixed the file is uploaded again.
create unique index files_accepted_once on ledger.files (tenant_id, sha256) where status = 'accepted';
create index on ledger.files (tenant_id, status, received_at);

create table ledger.statements (
  id              uuid primary key default gen_random_uuid(),
  tenant_id       uuid not null references tenants(id) on delete cascade,
  account_id      uuid not null,
  file_id         uuid not null,
  layout_id       text not null,
  period_from     date,
  period_to       date,
  opening         bigint not null,
  closing         bigint not null,
  opening_stated  boolean not null,
  closing_stated  boolean not null,
  running         boolean not null,
  created_at      timestamptz not null default now(),
  foreign key (tenant_id, account_id) references ledger.accounts (tenant_id, id),
  foreign key (tenant_id, file_id) references ledger.files (tenant_id, id),
  unique (file_id),
  unique (tenant_id, id)
);
create index on ledger.statements (tenant_id, account_id, period_to);

create table ledger.movements (
  id            uuid primary key default gen_random_uuid(),
  tenant_id     uuid not null references tenants(id) on delete cascade,
  account_id    uuid not null,
  statement_id  uuid not null,
  key           text not null,        -- deduplication key, see internal/ledger
  line          int not null,
  date          date not null,
  amount        bigint not null,       -- cents, credits positive
  balance       bigint,                -- running balance after the line, when the file has it
  description   text not null,
  reference     text not null default '',
  external_ref  text not null default '',
  gross         bigint,
  fees          jsonb,                 -- [{"kind": "...", "amount": cents}] documented by the source
  created_at    timestamptz not null default now(),
  foreign key (tenant_id, account_id) references ledger.accounts (tenant_id, id),
  foreign key (tenant_id, statement_id) references ledger.statements (tenant_id, id),
  unique (tenant_id, account_id, key)
);
create index on ledger.movements (tenant_id, account_id, date);

grant select, insert, update, delete on all tables in schema ledger to tramo_app;

alter table ledger.accounts   enable row level security;
alter table ledger.accounts   force row level security;
alter table ledger.files      enable row level security;
alter table ledger.files      force row level security;
alter table ledger.statements enable row level security;
alter table ledger.statements force row level security;
alter table ledger.movements  enable row level security;
alter table ledger.movements  force row level security;

create policy accounts_tenant on ledger.accounts to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy files_tenant on ledger.files to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy statements_tenant on ledger.statements to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
create policy movements_tenant on ledger.movements to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());

-- +goose Down
drop schema ledger cascade;
