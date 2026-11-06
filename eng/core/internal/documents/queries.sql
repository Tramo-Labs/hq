-- Queries of the documents module. `make gen` compiles them with sqlc into
-- internal/documents/store. Everything runs in tenant transactions
-- (db.InTenant): RLS filters by tenant even if a query forgets to.

-- name: TenantCUIT :one
-- The CUIT of the tenant in the transaction; it tells which side of an
-- invoice the tenant is on.
select cuit from tenants where id = app_current_tenant();

-- name: InsertInvoice :one
insert into documents.invoices (tenant_id, sha256, fields, trail, auto_eligible, min_confidence,
  issuer_cuit, doc_type, point_of_sale, number, issue_date, total, item_reason, direction_uncertain)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
returning id;

-- name: ListInvoices :many
-- Newest first. period is YYYY-MM in Argentina time (empty for all);
-- auto_eligible null for both; review keeps only those not eligible or
-- without an open item.
select id, issuer_cuit, doc_type, point_of_sale, number, issue_date, total, auto_eligible, min_confidence,
       fields, coalesce(item_reason, '')::text as item_reason, created_at
  from documents.invoices
 where (sqlc.arg(period)::text = ''
        or to_char(created_at at time zone 'America/Argentina/Buenos_Aires', 'YYYY-MM') = sqlc.arg(period)::text)
   and (sqlc.narg(auto_eligible)::boolean is null or auto_eligible = sqlc.narg(auto_eligible)::boolean)
   and (not sqlc.arg(review)::boolean or not auto_eligible or item_reason is not null)
 order by created_at desc, id
 limit sqlc.arg(max_rows)::int;

-- name: GetInvoice :one
select fields, trail, item_reason, direction_uncertain from documents.invoices where id = $1;
