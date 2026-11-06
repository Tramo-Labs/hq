-- +goose Up
-- Documents: supplier invoices with every field's source and confidence.
-- The key columns are filled only from trusted fields (see
-- internal/documents): a duplicate is caught by the database when the same
-- issuer, type, point of sale and number arrive twice.

create schema documents;
grant usage on schema documents to tramo_app;

create table documents.invoices (
  id              uuid primary key default gen_random_uuid(),
  tenant_id       uuid not null references tenants(id) on delete cascade,
  sha256          bytea not null,        -- of the uploaded document
  fields          jsonb not null,        -- {name: {Value, Source, Confidence, Conflict, Note}}
  trail           jsonb not null,        -- stages that ran, without values
  auto_eligible   boolean not null,
  min_confidence  real not null,
  issuer_cuit     text,
  doc_type        int,
  point_of_sale   int,
  number          bigint,
  issue_date      date,
  total           bigint,                -- cents
  constatacion    text not null default 'pending' check (constatacion in ('pending', 'approved', 'rejected')),
  storage_key     text,                  -- encrypted object in the bucket
  created_at      timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, sha256),
  unique (tenant_id, issuer_cuit, doc_type, point_of_sale, number)
);

grant select, insert, update, delete on all tables in schema documents to tramo_app;

alter table documents.invoices enable row level security;
alter table documents.invoices force row level security;
create policy invoices_tenant on documents.invoices to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());

-- +goose Down
drop schema documents cascade;
