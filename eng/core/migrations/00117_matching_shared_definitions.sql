-- +goose Up
-- Two definitions of matching that several queries share, moved into the
-- database so each exists once (queries are sqlc now, and sqlc cannot
-- splice SQL fragments). Both are security_invoker views: they run with the
-- caller's rights, so row level security of every table underneath still
-- filters by tenant.

-- A match is in effect when it settles its movements and items now:
-- automatic, approved in either tray, or linked by hand while that link is
-- in effect. Reverted decisions do not count (the *_state views). A view,
-- not a function: the planner inlines a view into each query, while a SQL
-- function with subqueries is never inlined and runs once per row (measured:
-- the Suggested tray went from about 58 ms to seconds).
create view matching.matches_in_effect with (security_invoker = true) as
  select m.*
    from matching.matches m
   where m.tray = 'auto'
      or exists (select 1 from matching.decision_state d
                  where d.tenant_id = m.tenant_id and d.match_id = m.id and d.action = 'approve')
      or (m.tray = 'manual' and exists (select 1 from matching.movement_decision_state s
                  where s.tenant_id = m.tenant_id and s.match_id = m.id and s.action = 'link'));
grant select on matching.matches_in_effect to tramo_app;

-- What waits in each tray, for the tray lists and the results panel alike,
-- by period (YYYY-MM):
--   - what the latest run of each period left pending, for movements with
--     no decision in effect (period: the run's);
--   - movements whose decision in effect hands them to a side, whatever
--     the runs say: the client's "other" or reject to the operator
--     (suggested), the operator's return to the client (exception), unless
--     a match in effect settles the movement now (period: the movement's
--     month).
-- Resolved and linked movements are in neither tray.
create view matching.waiting with (security_invoker = true) as
  with latest as (select distinct on (period) id, period from matching.runs
                   order by period, started_at desc, id),
       st as (select tenant_id, movement_id, action, note, match_id
                from matching.movement_decision_state where action is not null)
  select p.tenant_id, p.run_id, p.movement_id, p.tray, p.reason, p.candidates,
         ''::text as note, null::text as match_id, l.period
    from matching.pending p
    join latest l on l.id = p.run_id
   where not exists (select 1 from st where st.movement_id = p.movement_id)
  union all
  select st.tenant_id,
         coalesce((select r.id from matching.runs r where r.period = to_char(mv.date, 'YYYY-MM')
                    order by r.started_at desc, r.id limit 1),
                  (select r.id from matching.runs r order by r.started_at desc, r.id limit 1)),
         st.movement_id,
         case st.action when 'return' then 'exception' else 'suggested' end,
         case st.action when 'other' then 'client_other' when 'rejected' then 'client_rejected'
                        else 'operator_returned' end,
         '[]'::jsonb, st.note, case when st.action = 'rejected' then st.match_id end,
         to_char(mv.date, 'YYYY-MM')
    from st
    join ledger.movements mv on mv.id = st.movement_id
   where st.action in ('other', 'rejected', 'return')
     and not exists (select 1 from matching.matches_in_effect m where st.movement_id = any(m.movement_ids));
grant select on matching.waiting to tramo_app;

-- +goose Down
drop view matching.waiting;
drop view matching.matches_in_effect;
