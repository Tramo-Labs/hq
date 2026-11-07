-- Queries of the ledger module. `make gen` compiles them with sqlc into
-- internal/ledger/store. Everything runs in tenant transactions
-- (db.InTenant): RLS filters by tenant even if a query forgets to.

-- name: FileAccepted :one
select exists (select 1 from ledger.files where sha256 = $1 and status = 'accepted');

-- name: MovementsWithKeys :many
-- The stored movements of an account among the given dedupe keys.
select key, description from ledger.movements
 where account_id = sqlc.arg(account_id) and key = any(sqlc.arg(keys)::text[]);

-- name: LastStatement :one
-- The statement of the account that reaches furthest, for the continuity check.
select period_from, period_to, closing from ledger.statements
 where account_id = $1 and period_to is not null
 order by period_to desc, created_at desc
 limit 1;

-- name: InsertAcceptedFile :one
insert into ledger.files (tenant_id, account_id, sha256, filename, status, fingerprint, layout_id, uploaded_by, source, routed)
values (app_current_tenant(), $1, $2, $3, 'accepted', $4, $5, app_current_user(),
        sqlc.arg(source)::text, sqlc.narg(routed)::text)
returning id;

-- name: InsertQuarantinedFile :one
insert into ledger.files (tenant_id, account_id, sha256, filename, status, reason, detail, fingerprint, layout_id, uploaded_by,
                          closed_start, closed_end, source, routed)
values (app_current_tenant(), sqlc.narg(account_id)::uuid, $1, $2, 'quarantined', $3, $4, $5, $6, app_current_user(),
        sqlc.narg(closed_start)::date, sqlc.narg(closed_end)::date, sqlc.arg(source)::text, sqlc.narg(routed)::text)
returning id;

-- name: InsertStatement :one
insert into ledger.statements (tenant_id, account_id, file_id, layout_id, period_from, period_to,
  opening, closing, opening_stated, closing_stated, running)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
returning id;

-- name: InsertMovement :batchexec
-- A movement already stored under the same key is left as it is.
insert into ledger.movements (tenant_id, account_id, statement_id, key, line, date, amount,
  balance, description, reference, external_ref, gross, fees)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
on conflict (tenant_id, account_id, key) do nothing;

-- name: MovementsBetween :many
-- Every movement of the tenant dated from..to, inclusive (the full export).
select id, account_id, key, line, date, amount, balance, description, reference, external_ref, gross, fees
  from ledger.movements
 where date between sqlc.arg(date_from) and sqlc.arg(date_to)
 order by date, id;

-- name: InsertAccount :one
insert into ledger.accounts (tenant_id, bank, number, name) values (app_current_tenant(), $1, $2, $3)
returning id;

-- name: ListAccounts :many
select id, bank, coalesce(number, '')::text as number, name, currency from ledger.accounts order by created_at, id;

-- name: AccountsOfBank :many
-- What routing a mailed file needs: the tenant's accounts of a bank, and
-- whether each already has an accepted statement.
select a.id, coalesce(a.number, '')::text as number, a.currency,
       exists (select 1 from ledger.statements s where s.account_id = a.id) as has_statement
  from ledger.accounts a
 where a.bank = $1
 order by a.created_at, a.id;

-- name: GetAccount :one
select bank, coalesce(number, '')::text as number, name, currency from ledger.accounts where id = $1;

-- name: ListQuarantine :many
-- Quarantined files, newest first.
select id, account_id, coalesce(filename, '')::text as filename, reason::text as reason,
       coalesce(detail, '')::text as detail, coalesce(fingerprint, '')::text as fingerprint, received_at
  from ledger.files
 where status = 'quarantined'
 order by received_at desc, id
 limit sqlc.arg(max_rows)::int;

-- name: ListFiles :many
-- The "Archivos" list (F12), newest first. period is YYYY-MM received in
-- that month, Argentina time; status narrows by status. Empty means any.
select f.id, f.account_id, a.name as account_name, a.bank, coalesce(f.filename, '')::text as filename, f.status,
       coalesce(f.reason, '')::text as reason, coalesce(f.detail, '')::text as detail,
       coalesce(f.layout_id, '')::text as layout_id,
       st.period_from, st.period_to,
       (select count(*) from ledger.movements m where m.statement_id = st.id) as movements,
       f.received_at, f.uploaded_by, coalesce(u.email, '')::text as uploaded_by_email,
       f.closed_start, f.closed_end, f.source, f.routed
  from ledger.files f
  -- A file with no account (mailed, not routed) is listed too.
  left join ledger.accounts a on a.id = f.account_id
  left join ledger.statements st on st.file_id = f.id
  left join users u on u.id = f.uploaded_by
 where (sqlc.arg(period)::text = ''
        or to_char(f.received_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = sqlc.arg(period)::text)
   and (sqlc.arg(status)::text = '' or f.status = sqlc.arg(status)::text)
 order by f.received_at desc, f.id
 limit sqlc.arg(max_rows)::int;

-- name: ListMovements :many
-- Movements dated from..to, optionally of one account, in date, account
-- and line order, with the file each came from (F12).
select m.id, m.account_id, st.file_id, a.name as account_name, a.bank, m.line, m.date, m.amount, m.balance,
       m.description, m.reference, m.external_ref
  from ledger.movements m
  join ledger.accounts a on a.id = m.account_id
  join ledger.statements st on st.id = m.statement_id
 where m.date between sqlc.arg(date_from) and sqlc.arg(date_to)
   and (sqlc.narg(account_id)::uuid is null or m.account_id = sqlc.narg(account_id)::uuid)
 order by m.date, a.name, m.account_id, st.created_at, m.line
 limit sqlc.arg(max_rows)::int;

-- name: StatementSpans :many
-- Every dated statement of the tenant, for the coverage of a period (Gaps).
select account_id, period_from::date as period_from, period_to::date as period_to, opening, closing
  from ledger.statements
 where period_from is not null and period_to is not null
 order by account_id, period_from, period_to;
