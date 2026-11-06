-- Queries of the matching module. `make gen` compiles them with sqlc into
-- internal/matching/store. Everything runs in tenant transactions
-- (db.InTenant): RLS filters by tenant even if a query forgets to.
--
-- Two definitions are shared by several queries and live in the database
-- (migration 00117) so each exists once: matching.matches_in_effect (a
-- match that settles its movements and items now) and matching.waiting
-- (what waits in each tray, by period). The tray lists and the results
-- panel both read matching.waiting, so they cannot disagree.

-- Items --------------------------------------------------------------------

-- name: UpsertItem :one
-- Loading the same source id again updates the row instead of adding one.
insert into matching.items (tenant_id, source, source_id, kind, date, due, amount, refs, confidence, party)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
on conflict (tenant_id, source, source_id) do update set kind = excluded.kind, date = excluded.date,
  due = excluded.due, amount = excluded.amount, refs = excluded.refs,
  confidence = excluded.confidence, party = excluded.party
returning id;

-- name: UpsertInvoiceItem :exec
-- The open item of an invoice: source "document", source id the invoice id.
insert into matching.items (tenant_id, source, source_id, invoice_id, kind, date, amount, refs, confidence, party)
values (app_current_tenant(), 'document', sqlc.arg(invoice_id)::uuid::text, sqlc.arg(invoice_id)::uuid,
        sqlc.arg(kind), sqlc.arg(date), sqlc.arg(amount), sqlc.arg(refs), sqlc.arg(confidence), sqlc.arg(party))
on conflict (tenant_id, source, source_id) do update set kind = excluded.kind, date = excluded.date,
  amount = excluded.amount, refs = excluded.refs, confidence = excluded.confidence, party = excluded.party;

-- name: OpenItems :many
-- Items no match in effect settles, oldest first then by source id. period
-- YYYY-MM keeps the items dated in that month (empty: all); direction "in"
-- keeps positive amounts, "out" negative (empty: both).
-- The settled items are unnested and read as one "not in" list, which
-- Postgres builds once as a hash in every plan. Measured on the benchmark:
-- "i.id = any(item_ids)" compared every item with every match (0.8 s, 2 to
-- 5 s with the generic plan a prepared statement gets after five runs);
-- "not exists" on the unnested ids was fast with a custom plan and 7 to 9 s
-- with the generic one. Nulls are left out of the list: one null would make
-- "not in" keep no item at all.
select i.id, i.source, i.source_id, i.kind, i.date, i.due, i.amount, i.refs, i.party, i.confidence, i.invoice_id
  from matching.items i
 where i.id not in (select settled.item_id
                      from matching.matches_in_effect m cross join lateral unnest(m.item_ids) as settled(item_id)
                     where settled.item_id is not null)
   and (sqlc.arg(period)::text = '' or to_char(i.date, 'YYYY-MM') = sqlc.arg(period)::text)
   and (sqlc.arg(direction)::text = ''
        or (sqlc.arg(direction)::text = 'in' and i.amount > 0)
        or (sqlc.arg(direction)::text = 'out' and i.amount < 0))
 order by i.date, i.source_id, i.id
 limit sqlc.arg(max_rows)::int;

-- name: ItemsCountAndSum :one
select count(*), coalesce(sum(amount), 0)::bigint as total
  from matching.items where id = any(sqlc.arg(ids)::uuid[]);

-- name: AnyItemTaken :one
-- Whether a match in effect already settles any of the items.
select exists (select 1 from matching.matches_in_effect m where m.item_ids && sqlc.arg(ids)::uuid[]);

-- Engine input -------------------------------------------------------------

-- name: InputMovements :many
select id, account_id, date, amount, description, reference, external_ref, gross, fees
  from ledger.movements
 where date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date;

-- name: InputItems :many
-- Items from long before the period stay candidates for the reference pass
-- (Duotach case 5); the date window is the engine's.
select id, kind, date, due, amount, refs, confidence, party
  from matching.items
 where coalesce(due, date) <= sqlc.arg(until)::date;

-- name: InputMatches :many
-- Every stored match with whether it is in effect (settled) and whether a
-- decision in effect turned it down: rejected in either tray, or "other"
-- from the client. Escalated and undecided is neither.
select m.rule_id, m.rule_version, m.tray, m.movement_ids, m.item_ids,
       exists (select 1 from matching.matches_in_effect e where e.tenant_id = m.tenant_id and e.id = m.id) as in_effect,
       exists (select 1 from matching.decision_state d
                where d.tenant_id = m.tenant_id and d.match_id = m.id and d.action in ('reject', 'other')) as turned_down
  from matching.matches m;

-- Runs ---------------------------------------------------------------------

-- name: InsertRun :one
insert into matching.runs (tenant_id, period, config) values (app_current_tenant(), $1, $2) returning id;

-- name: InsertMatch :execrows
-- A match already stored (same id) is left as it is.
insert into matching.matches (tenant_id, id, run_id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
on conflict (tenant_id, id) do nothing;

-- name: InsertManualMatch :exec
-- A link by hand: no run behind it.
insert into matching.matches (tenant_id, id, run_id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence)
values (app_current_tenant(), $1, null, $2, $3, 'manual', $4, $5, '[]', $6)
on conflict (tenant_id, id) do nothing;

-- name: InsertPending :exec
insert into matching.pending (tenant_id, run_id, movement_id, tray, reason, candidates)
values (app_current_tenant(), $1, $2, $3, $4, $5);

-- name: MovementDays :many
-- The days that have movements in a range, both ends included.
select distinct date from ledger.movements
 where date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date
 order by date;

-- name: MovementDatesOf :many
select id, date from ledger.movements where id = any(sqlc.arg(ids)::uuid[]);

-- name: LatestRunOf :one
select id from matching.runs where period = $1 order by started_at desc, id limit 1;

-- name: CarryPending :execrows
-- A run of a month with a closed period keeps, for the movements of the
-- closed days, what the run before it left pending: a run never changes
-- what a close froze.
insert into matching.pending (tenant_id, run_id, movement_id, tray, reason, candidates)
select p.tenant_id, sqlc.arg(to_run)::uuid, p.movement_id, p.tray, p.reason, p.candidates
  from matching.pending p join ledger.movements l on l.id = p.movement_id
 where p.run_id = sqlc.arg(from_run)::uuid and l.date = any(sqlc.arg(days)::date[]);

-- Roles --------------------------------------------------------------------

-- name: MemberRole :one
-- The caller's role from memberships, not from the token.
select role::text from memberships where user_id = app_current_user() and tenant_id = app_current_tenant();

-- Match decisions ----------------------------------------------------------

-- name: MatchTrayAndPeriods :one
select m.tray, coalesce(array(select jsonb_array_elements_text(m.evidence->'movement_periods')), '{}')::text[] as periods,
       coalesce(array(select l.date from ledger.movements l where l.id = any(m.movement_ids)), '{}')::date[] as dates
  from matching.matches m where m.id = $1;

-- name: MatchDecisionStates :many
select tray, id, revision, coalesce(action, '')::text as action from matching.decision_state where match_id = $1;

-- name: DecisionByKey :one
select match_id, action, user_id, role, at, note, tray from matching.decisions where key = $1;

-- name: InsertDecision :one
insert into matching.decisions (tenant_id, key, match_id, tray, revision, action, user_id, role, note, snapshot, reverts)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
returning at;

-- name: GetMatch :one
select id, rule_id, rule_version, tray, movement_ids, item_ids, adjustments, evidence
  from matching.matches where id = $1;

-- name: UndecidedMatches :many
-- auto: nobody decides, listed for review. suggested: no operator decision
-- yet. exception: escalated, no client decision yet. date_from and
-- date_to (both included) keep the matches with a movement dated in that
-- range (null: all).
select m.id, m.rule_id, m.rule_version, m.tray, m.movement_ids, m.item_ids, m.adjustments, m.evidence
  from matching.matches m
 where case sqlc.arg(tray)::text
         when 'auto' then m.tray = 'auto'
         when 'suggested' then m.tray = 'suggested' and not exists (select 1 from matching.decision_state d
           where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested' and d.action is not null)
         when 'exception' then exists (select 1 from matching.decision_state d
           where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested' and d.action = 'escalate')
           and not exists (select 1 from matching.decision_state d
           where d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'exception' and d.action is not null)
         else false end
   and (sqlc.narg(date_from)::date is null or exists (select 1 from ledger.movements mv
         where mv.id = any(m.movement_ids) and mv.date between sqlc.narg(date_from)::date and sqlc.narg(date_to)::date))
 order by m.created_at, m.id
 limit sqlc.arg(max_rows)::int;

-- Movement decisions -------------------------------------------------------

-- name: MovementState :one
-- The decision in effect on a movement (empty action when none).
select coalesce(id, 0)::bigint as id, revision, coalesce(action, '')::text as action,
       coalesce(match_id, '')::text as match_id, coalesce(role, '')::text as role
  from matching.movement_decision_state where movement_id = $1;

-- name: MovementDecisionByKey :one
select movement_id, action, user_id, role, note, match_id, revision, at
  from matching.movement_decisions where key = $1;

-- name: MovementAmountDate :one
select amount, date from ledger.movements where id = $1;

-- name: WaitsWithNoCandidate :one
-- Whether the latest run of any period left the movement in Exceptions
-- with no candidate.
with latest as (select distinct on (period) id from matching.runs order by period, started_at desc, id)
select exists (select 1 from matching.pending
                where run_id in (select id from latest) and movement_id = $1
                  and tray = 'exception' and reason = 'no_candidate');

-- name: InsertMovementDecision :one
insert into matching.movement_decisions (tenant_id, key, movement_id, revision, action, match_id, note, user_id, role, reverts)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9)
returning at;

-- name: MovementDecisionMatch :one
select match_id from matching.movement_decisions where id = $1;

-- Trays and panel ----------------------------------------------------------

-- name: Waiting :many
-- What waits in a tray (matching.waiting), with the movement itself, by
-- the movement's date when date_from and date_to (both included) are set
-- (the month of the run that left it pending is the movement's month).
select w.run_id, w.movement_id, w.tray, w.reason, w.candidates, w.note, coalesce(w.match_id, '')::text as match_id,
       l.date, l.amount, l.description, a.id as account_id, a.name as account_name, a.bank
  from matching.waiting w
  join ledger.movements l on l.id = w.movement_id
  join ledger.accounts a on a.id = l.account_id
 where w.tray = sqlc.arg(tray)::text
   and (sqlc.narg(date_from)::date is null or l.date between sqlc.narg(date_from)::date and sqlc.narg(date_to)::date)
 order by w.movement_id
 limit sqlc.arg(max_rows)::int;

-- name: MonthCounts :one
select (select count(*) from ledger.movements where date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date) as movements,
       -- A run covers its whole month: the range has a run when a run's
       -- month overlaps it.
       exists (select 1 from matching.runs
                where to_date(period, 'YYYY-MM') <= sqlc.arg(date_to)::date
                  and (to_date(period, 'YYYY-MM') + interval '1 month')::date > sqlc.arg(date_from)::date) as has_run,
       (select count(distinct l.id) from matching.matches m
          join ledger.movements l on l.id = any(m.movement_ids)
         where m.tray = 'auto' and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date) as auto_movements;

-- name: SuggestedDecided :one
-- Suggested matches of the month decided by the operator, by the decision
-- in effect.
select count(*) filter (where d.action = 'approve') as approved, count(*) filter (where d.action = 'reject') as rejected
  from matching.matches m
  join matching.decision_state d on d.tenant_id = m.tenant_id and d.match_id = m.id and d.tray = 'suggested'
 where m.tray = 'suggested'
   and exists (select 1 from ledger.movements l
                where l.id = any(m.movement_ids) and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date);

-- name: OpenExceptionMovements :one
-- The movements the client's Exceptions list shows for the range (by the
-- movement's date, which is in the month of the run that left it pending),
-- and the oldest date among them (empty when there are none).
select count(*), coalesce(to_char(min(l.date), 'YYYY-MM-DD'), '')::text as oldest
  from matching.waiting w join ledger.movements l on l.id = w.movement_id
 where w.tray = 'exception' and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date;

-- name: OpenEscalatedMatches :one
-- Escalated matches of the month the client has not decided.
select count(*) from matching.matches m
 where exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id
                and d.tray = 'suggested' and d.action = 'escalate')
   and not exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id
                and d.tray = 'exception' and d.action is not null)
   and exists (select 1 from ledger.movements l
                where l.id = any(m.movement_ids) and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date);

-- name: SuggestedPendingInRange :one
-- What blocks a close: the Suggested tray of the range with no decision in
-- effect. Suggested matches the operator has not decided (an escalated one
-- has a decision, and waits in Exceptions), plus the movements waiting in
-- Suggested (ambiguous, the client's "other" or reject). By the movement's
-- date, both included; a match counts when any of its movements falls in.
select ((select count(*) from matching.matches m
          where m.tray = 'suggested'
            and not exists (select 1 from matching.decision_state d where d.tenant_id = m.tenant_id and d.match_id = m.id
                             and d.tray = 'suggested' and d.action is not null)
            and exists (select 1 from ledger.movements l where l.id = any(m.movement_ids)
                         and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date))
      + (select count(*) from matching.waiting w join ledger.movements l on l.id = w.movement_id
          where w.tray = 'suggested' and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date))::int as pending;

-- name: SuggestedPendingByReason :many
-- What the operator's Suggested list shows for the range, by reason.
select w.reason, count(*) from matching.waiting w join ledger.movements l on l.id = w.movement_id
 where w.tray = 'suggested' and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date
 group by 1;

-- name: InvoiceCounts :one
select exists (select 1 from documents.invoices) as ever,
       count(*) filter (where (created_at at time zone 'America/Argentina/Buenos_Aires')::date
                          between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date) as month,
       count(*) filter (where (created_at at time zone 'America/Argentina/Buenos_Aires')::date
                          between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date
                          and (not auto_eligible or item_reason is not null)) as held
  from documents.invoices;

-- name: MatchExportRows :many
-- Matches with a movement dated from..to and the decision in effect: the
-- client's in Exceptions, else the operator's in Suggested, or the
-- client's link for a manual match. Without operator, only what the
-- client may see: automatic, linked by hand, approved, sent to Exceptions.
-- When and which revision come per side (decision, link): sqlc would type
-- a coalesce of the two as never null.
select m.id, m.rule_id, m.rule_version, m.tray, m.movement_ids::text[] as movement_ids, m.item_ids::text[] as item_ids,
       m.adjustments, m.evidence,
       coalesce(dx.tray, ds.tray, '')::text as decision_tray, coalesce(dx.action, ds.action, ms.action, '')::text as decision,
       coalesce(dd.user_id::text, md.user_id::text, '')::text as decided_by, coalesce(dd.role, md.role, '')::text as decided_role,
       dd.at as decision_at, md.at as link_at, dd.revision as decision_revision, md.revision as link_revision
  from matching.matches m
  left join matching.decision_state dx on dx.tenant_id = m.tenant_id and dx.match_id = m.id
        and dx.tray = 'exception' and dx.action is not null
  left join matching.decision_state ds on ds.tenant_id = m.tenant_id and ds.match_id = m.id
        and ds.tray = 'suggested' and ds.action is not null
  left join matching.decisions dd on dd.id = coalesce(dx.id, ds.id)
  left join matching.movement_decision_state ms on ms.tenant_id = m.tenant_id and ms.match_id = m.id and ms.action = 'link'
  left join matching.movement_decisions md on md.id = ms.id
 where exists (select 1 from ledger.movements l
                where l.id = any(m.movement_ids) and l.date between sqlc.arg(date_from)::date and sqlc.arg(date_to)::date)
   and (sqlc.arg(operator)::boolean or m.tray = 'auto' or ms.action = 'link'
        or ds.action in ('approve', 'escalate') or dx.action is not null)
 order by m.created_at, m.id;
