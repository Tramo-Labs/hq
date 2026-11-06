-- +goose Up
-- F21: an invoice whose trusted issuer and receiver are both someone else
-- is not this tenant's. It generates no item and is listed to review.
alter table documents.invoices drop constraint invoices_item_reason_check;
alter table documents.invoices add constraint invoices_item_reason_check
  check (item_reason in ('no_tenant_cuit', 'missing_total_or_date', 'foreign_currency', 'unknown_direction', 'not_this_tenant'));

-- +goose Down
alter table documents.invoices drop constraint invoices_item_reason_check;
alter table documents.invoices add constraint invoices_item_reason_check
  check (item_reason in ('no_tenant_cuit', 'missing_total_or_date', 'foreign_currency', 'unknown_direction'));
