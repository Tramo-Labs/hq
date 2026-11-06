-- Queries of the inbox module (sqlc). All run under a tenant scope (RLS);
-- the one lookup before a scope exists is db.InboxTenant, not here.

-- name: MemberRole :one
-- The caller's role from memberships, not from the token.
select role::text from memberships where user_id = app_current_user() and tenant_id = app_current_tenant();

-- name: ActiveAddress :one
select id, code, created_at from inbox.addresses
 where tenant_id = app_current_tenant() and revoked_at is null;

-- name: RevokeAddress :exec
update inbox.addresses set revoked_at = now()
 where tenant_id = app_current_tenant() and revoked_at is null;

-- name: InsertAddress :one
insert into inbox.addresses (tenant_id, code, created_by)
values (app_current_tenant(), $1, app_current_user())
returning id, code, created_at;

-- name: ActiveSenders :many
select id, email, added_by, added_at from inbox.senders
 where tenant_id = app_current_tenant() and removed_at is null
 order by email;

-- name: InsertSender :one
-- An email already authorized inserts nothing (no rows).
insert into inbox.senders (tenant_id, email, added_by)
values (app_current_tenant(), $1, app_current_user())
on conflict (tenant_id, email) where removed_at is null do nothing
returning id, email, added_by, added_at;

-- name: RemoveSender :execrows
update inbox.senders set removed_at = now()
 where tenant_id = app_current_tenant() and id = $1 and removed_at is null;

-- name: SenderAuthorized :one
select exists (select 1 from inbox.senders
 where tenant_id = app_current_tenant() and email = $1 and removed_at is null);

-- name: ReceiptByMessage :one
select id, received_at, sender, status, reason, attachments from inbox.receipts
 where tenant_id = app_current_tenant() and message_id_hash = $1;

-- name: CountDay :one
-- Receipts of the day: all, accepted ones, and accepted ones from sender.
select count(*)::int as total,
       count(*) filter (where status = 'accepted')::int as accepted,
       count(*) filter (where status = 'accepted' and sender = sqlc.arg(sender))::int as from_sender
  from inbox.receipts
 where tenant_id = app_current_tenant() and day = sqlc.arg(day);

-- name: InsertReceipt :one
-- A message already received (same Message-ID) inserts nothing (no rows).
insert into inbox.receipts (tenant_id, message_id_hash, received_at, day, sender, status, reason, attachments)
values (app_current_tenant(), $1, $2, $3, $4, $5, $6, $7)
on conflict (tenant_id, message_id_hash) do nothing
returning id, received_at, sender, status, reason, attachments;

-- name: RecentReceipts :many
select id, received_at, sender, status, reason, attachments from inbox.receipts
 where tenant_id = app_current_tenant()
 order by received_at desc
 limit sqlc.arg(lim);

-- name: LockReceive :exec
-- Serializes the receipts of one tenant, so the daily limits hold under
-- concurrent deliveries.
select pg_advisory_xact_lock(hashtextextended('inbox:' || app_current_tenant()::text, 0));
