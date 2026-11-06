-- +goose Up
-- F12: the "Archivos" list says who uploaded each file. Null for files
-- stored before this column, or uploaded by a process with no user.
alter table ledger.files add column uploaded_by uuid references users(id);

-- +goose Down
alter table ledger.files drop column uploaded_by;
