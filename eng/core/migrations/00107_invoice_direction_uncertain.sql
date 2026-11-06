-- +goose Up
-- An invoice whose open item has an uncertain direction (read without the
-- ARCA QR) is not eligible for automatic: the invoice and its item say the
-- same thing, and it counts in "para revisar".
alter table documents.invoices add column direction_uncertain boolean not null default false;

-- +goose Down
alter table documents.invoices drop column direction_uncertain;
