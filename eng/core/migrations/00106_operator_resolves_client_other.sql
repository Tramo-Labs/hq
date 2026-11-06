-- +goose Up
-- F4: a movement the client marked "other" goes back to the operator, and
-- the operator needs a way to answer it: 'resolve' (what was done with it),
-- 'link' (to open items that add up exactly) or 'return' (back to the
-- client, with what the operator needs). Resolve and return need a note.
-- These rows stack on top of the client's 'other': reverting the
-- operator's row puts the client's back in effect instead of erasing it.
alter table matching.movement_decisions drop constraint movement_decisions_action_check;
alter table matching.movement_decisions add constraint movement_decisions_action_check
  check (action in ('other', 'link', 'resolve', 'return', 'revert'));
alter table matching.movement_decisions add constraint movement_decisions_operator_note
  check (action not in ('resolve', 'return') or note <> '');

-- The state is the latest revision (to number the next one) and the
-- decision in effect: the latest row that is not a reversion and that
-- nobody reverted. A null action means nothing is in effect.
drop view matching.movement_decision_state;
create view matching.movement_decision_state with (security_invoker = true) as
  select l.tenant_id, l.movement_id, e.id, l.revision, e.match_id, e.note, e.action, e.role
    from (select distinct on (tenant_id, movement_id) tenant_id, movement_id, revision
            from matching.movement_decisions
           order by tenant_id, movement_id, revision desc) l
    left join lateral (
      select d.id, d.match_id, d.note, d.action, d.role
        from matching.movement_decisions d
       where d.tenant_id = l.tenant_id and d.movement_id = l.movement_id and d.action <> 'revert'
         and not exists (select 1 from matching.movement_decisions r
                          where r.tenant_id = d.tenant_id and r.reverts = d.id)
       order by d.revision desc
       limit 1) e on true;
grant select on matching.movement_decision_state to tramo_app;

-- +goose Down
drop view matching.movement_decision_state;
create view matching.movement_decision_state with (security_invoker = true) as
  select distinct on (tenant_id, movement_id)
         tenant_id, movement_id, id, revision, match_id, note,
         case when action = 'revert' then null else action end as action
    from matching.movement_decisions
   order by tenant_id, movement_id, revision desc;
grant select on matching.movement_decision_state to tramo_app;
alter table matching.movement_decisions drop constraint movement_decisions_operator_note;
alter table matching.movement_decisions drop constraint movement_decisions_action_check;
alter table matching.movement_decisions add constraint movement_decisions_action_check
  check (action in ('other', 'link', 'revert'));
