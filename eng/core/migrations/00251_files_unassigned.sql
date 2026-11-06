-- +goose Up
-- Statements that arrive with no account (the per tenant inbox, D-22):
-- ingestion routes them by bank and account number. What it cannot route
-- is quarantined with no account; an accepted file always has one.
-- source says how the file came in; routed says how an account was found
-- for a mailed file: by the number the file states, or because the tenant
-- has a single account of that bank that already has a statement.
alter table ledger.files alter column account_id drop not null;
alter table ledger.files add column source text not null default 'upload'
  check (source in ('upload', 'mail'));
alter table ledger.files add column routed text
  check (routed in ('number', 'single_account'));
alter table ledger.files add constraint files_account_when_accepted check (
  status <> 'accepted' or account_id is not null);
alter table ledger.files add constraint files_routed_only_mail check (
  routed is null or (source = 'mail' and account_id is not null));

-- +goose Down
delete from ledger.files where account_id is null;
alter table ledger.files drop constraint files_routed_only_mail;
alter table ledger.files drop constraint files_account_when_accepted;
alter table ledger.files drop column routed;
alter table ledger.files drop column source;
alter table ledger.files alter column account_id set not null;
