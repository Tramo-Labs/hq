-- +goose Up
-- A client's reject of a match in Exceptions sends each movement of the
-- match back to the operator: one 'rejected' row per movement, with the
-- client's reason and the rejected match. It shares the turn and the
-- reversion rules of 'other' (migration 00106); it is reverted on the match.
alter table matching.movement_decisions drop constraint movement_decisions_action_check;
alter table matching.movement_decisions add constraint movement_decisions_action_check
  check (action in ('other', 'link', 'resolve', 'return', 'rejected', 'revert'));
alter table matching.movement_decisions add constraint movement_decisions_rejected_note
  check (action <> 'rejected' or note <> '');

-- The unnamed check of 00103 tied match_id to 'link' only; a 'rejected'
-- row carries the match it rejects too. Found by definition, not by the
-- name Postgres gave it.
-- +goose StatementBegin
do $$
declare c text;
begin
  select conname into strict c from pg_constraint
   where conrelid = 'matching.movement_decisions'::regclass and contype = 'c'
     and pg_get_constraintdef(oid) = 'CHECK (((action = ''link''::text) = (match_id IS NOT NULL)))';
  execute format('alter table matching.movement_decisions drop constraint %I', c);
end $$;
-- +goose StatementEnd
alter table matching.movement_decisions add constraint movement_decisions_match_id
  check ((action in ('link', 'rejected')) = (match_id is not null));

-- +goose Down
alter table matching.movement_decisions drop constraint movement_decisions_match_id;
alter table matching.movement_decisions add constraint movement_decisions_check1
  check ((action = 'link') = (match_id is not null));
alter table matching.movement_decisions drop constraint movement_decisions_rejected_note;
alter table matching.movement_decisions drop constraint movement_decisions_action_check;
alter table matching.movement_decisions add constraint movement_decisions_action_check
  check (action in ('other', 'link', 'resolve', 'return', 'revert'));
