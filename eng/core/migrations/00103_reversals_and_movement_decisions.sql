-- +goose Up
-- Reversal as a new recorded action, and client decisions on movements
-- without any candidate (product spec section 4).
--
-- Decisions keep being append only. Each decision on a match and tray has a
-- revision; reverting is a new row with action 'revert' that undoes the
-- latest one. The decision in effect for a match and tray is its latest
-- revision unless that one is a revert. Two decisions at once collide on
-- the revision and the second one fails.

alter table matching.decisions add column revision int not null default 1;
alter table matching.decisions add column reverts bigint references matching.decisions(id);
alter table matching.decisions drop constraint decisions_tenant_id_match_id_tray_key;
alter table matching.decisions add constraint decisions_tenant_id_match_id_tray_revision_key
  unique (tenant_id, match_id, tray, revision);
alter table matching.decisions drop constraint decisions_action_check;
alter table matching.decisions add constraint decisions_action_check
  check (action in ('approve', 'reject', 'escalate', 'other', 'revert'));
alter table matching.decisions add constraint decisions_revert_points_back
  check ((action = 'revert') = (reverts is not null));

-- The decision in effect per match and tray. security_invoker makes the
-- caller's row level security apply through the view.
create view matching.decision_state with (security_invoker = true) as
  select distinct on (tenant_id, match_id, tray)
         tenant_id, match_id, tray, id, revision,
         case when action = 'revert' then null else action end as action
    from matching.decisions
   order by tenant_id, match_id, tray, revision desc;
grant select on matching.decision_state to tramo_app;

-- Matches linked by hand by the client have no run behind them.
alter table matching.matches alter column run_id drop not null;
alter table matching.matches drop constraint matches_tray_check;
alter table matching.matches add constraint matches_tray_check check (tray in ('auto', 'suggested', 'manual'));
alter table matching.matches add constraint matches_manual_has_no_run check ((tray = 'manual') = (run_id is null));

alter table ledger.movements add constraint movements_tenant_id_id_key unique (tenant_id, id);

-- Client decisions on a movement of the Exceptions tray with no candidate:
-- 'other' (it is something else, note required, back to the operator),
-- 'link' (linked by hand to open items adding up exactly to the movement;
-- the manual match is match_id), or 'revert' of the latest one.
create table matching.movement_decisions (
  id           bigint generated always as identity primary key,
  tenant_id    uuid not null references tenants(id) on delete cascade,
  key          uuid not null,
  movement_id  uuid not null,
  revision     int not null,
  action       text not null check (action in ('other', 'link', 'revert')),
  match_id     text,
  note         text not null default '',
  user_id      uuid not null references users(id),
  role         text not null,
  at           timestamptz not null default now(),
  reverts      bigint references matching.movement_decisions(id),
  check (action <> 'other' or note <> ''),
  check ((action = 'link') = (match_id is not null)),
  check ((action = 'revert') = (reverts is not null)),
  unique (tenant_id, key),
  unique (tenant_id, movement_id, revision),
  foreign key (tenant_id, movement_id) references ledger.movements (tenant_id, id),
  foreign key (tenant_id, match_id) references matching.matches (tenant_id, id)
);

-- +goose StatementBegin
create function matching.movement_decisions_immutable() returns trigger language plpgsql as $$
begin
  raise exception 'matching.movement_decisions is append only';
end $$;
-- +goose StatementEnd
create trigger movement_decisions_no_update before update or delete on matching.movement_decisions
  for each row execute function matching.movement_decisions_immutable();

grant select, insert on matching.movement_decisions to tramo_app;
alter table matching.movement_decisions enable row level security;
alter table matching.movement_decisions force row level security;
create policy movement_decisions_tenant on matching.movement_decisions to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());

create view matching.movement_decision_state with (security_invoker = true) as
  select distinct on (tenant_id, movement_id)
         tenant_id, movement_id, id, revision, match_id, note,
         case when action = 'revert' then null else action end as action
    from matching.movement_decisions
   order by tenant_id, movement_id, revision desc;
grant select on matching.movement_decision_state to tramo_app;

-- +goose Down
drop view matching.movement_decision_state;
drop table matching.movement_decisions;
drop function matching.movement_decisions_immutable();
alter table ledger.movements drop constraint movements_tenant_id_id_key;
alter table matching.matches drop constraint matches_manual_has_no_run;
alter table matching.matches drop constraint matches_tray_check;
alter table matching.matches add constraint matches_tray_check check (tray in ('auto', 'suggested'));
alter table matching.matches alter column run_id set not null;
drop view matching.decision_state;
alter table matching.decisions drop constraint decisions_revert_points_back;
alter table matching.decisions drop constraint decisions_action_check;
alter table matching.decisions add constraint decisions_action_check
  check (action in ('approve', 'reject', 'escalate', 'other'));
alter table matching.decisions drop constraint decisions_tenant_id_match_id_tray_revision_key;
alter table matching.decisions add constraint decisions_tenant_id_match_id_tray_key unique (tenant_id, match_id, tray);
alter table matching.decisions drop column reverts;
alter table matching.decisions drop column revision;
