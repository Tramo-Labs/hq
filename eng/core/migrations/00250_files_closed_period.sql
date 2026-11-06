-- +goose Up
-- A statement that brings new movements dated in a closed period is
-- quarantined as period_closed (nothing enters). The file keeps the closed
-- period it hit, so the "Archivos" list can name it. Half open, like
-- closes.closes: [closed_start, closed_end).
alter table ledger.files add column closed_start date;
alter table ledger.files add column closed_end date;
alter table ledger.files add constraint files_closed_period check (
  (closed_start is null) = (closed_end is null)
  and (closed_start is null or (reason = 'period_closed' and closed_end > closed_start)));

-- +goose Down
alter table ledger.files drop constraint files_closed_period;
alter table ledger.files drop column closed_end;
alter table ledger.files drop column closed_start;
