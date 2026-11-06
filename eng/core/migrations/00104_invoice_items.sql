-- +goose Up
-- Invoices generate the open items matching crosses against (F2).
--
-- documents.invoices.item_reason says why an invoice generated no item
-- (null when it did). matching.items.invoice_id ties an item to its
-- invoice: the item follows the invoice (deleted with it), and an item that
-- a match in effect settles cannot change or disappear in silence.

alter table documents.invoices add column item_reason text
  check (item_reason in ('no_tenant_cuit', 'missing_total_or_date', 'foreign_currency', 'unknown_direction'));

alter table matching.items add column invoice_id uuid;
alter table matching.items add constraint items_invoice_fk
  foreign key (tenant_id, invoice_id) references documents.invoices (tenant_id, id) on delete cascade;
alter table matching.items add constraint items_one_per_invoice unique (tenant_id, invoice_id);

-- An item is settled while a match in effect holds it: automatic, approved
-- in either tray, or linked by hand. Changing its amount, dates, refs,
-- kind, party or confidence, or deleting it (directly or with its invoice),
-- is refused until that match is reverted.
-- +goose StatementBegin
create function matching.items_settled_guard() returns trigger language plpgsql as $$
declare
  holder text;
begin
  if tg_op = 'UPDATE' and (new.amount, new.date, new.due, new.kind, new.refs, new.confidence, new.party)
       is not distinct from (old.amount, old.date, old.due, old.kind, old.refs, old.confidence, old.party) then
    return new;
  end if;
  select m.id into holder from matching.matches m
   where m.tenant_id = old.tenant_id and old.id = any(m.item_ids)
     and (m.tray = 'auto'
          or exists (select 1 from matching.decision_state d
                      where d.tenant_id = m.tenant_id and d.match_id = m.id and d.action = 'approve')
          or (m.tray = 'manual' and exists (select 1 from matching.movement_decision_state s
                      where s.tenant_id = m.tenant_id and s.match_id = m.id and s.action = 'link')))
   limit 1;
  if holder is not null then
    raise exception 'matching.items: item % is settled by match %; revert the match first', old.id, holder
      using errcode = 'P0001';
  end if;
  if tg_op = 'DELETE' then
    return old;
  end if;
  return new;
end $$;
-- +goose StatementEnd
create trigger items_settled_guard before update or delete on matching.items
  for each row execute function matching.items_settled_guard();

-- +goose Down
drop trigger items_settled_guard on matching.items;
drop function matching.items_settled_guard();
alter table matching.items drop constraint items_one_per_invoice;
alter table matching.items drop constraint items_invoice_fk;
alter table matching.items drop column invoice_id;
alter table documents.invoices drop column item_reason;
