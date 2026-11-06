-- Queries of the identity module. `make gen` compiles them with sqlc into
-- internal/identity/store. Registration, login and refresh tokens run in
-- system transactions (no tenant yet, or tables tramo_app cannot read);
-- member management runs in tenant transactions under RLS.

-- name: CreateUser :one
insert into users (email, password_hash) values ($1, $2) returning id;

-- name: EnsureUser :one
-- Creates a user without password (magic link later) or returns the existing one.
insert into users (email) values ($1)
on conflict (email) do update set email = excluded.email
returning id;

-- name: CreateTenant :one
insert into tenants (slug, name, cuit) values ($1, $2, $3) returning id;

-- name: CreateMembership :exec
insert into memberships (user_id, tenant_id, role) values ($1, $2, $3);

-- name: GetUserForLogin :one
select id, password_hash from users where email = $1;

-- name: GetUserEmail :one
select email from users where id = $1;

-- name: ListMembershipsForUser :many
select m.tenant_id, t.name as tenant_name, m.role
from memberships m join tenants t on t.id = m.tenant_id
where m.user_id = $1
order by t.created_at, t.id;

-- name: GetMembershipRole :one
select role from memberships where user_id = $1 and tenant_id = $2;

-- name: ListMembers :many
select u.id, u.email, m.role
from memberships m join users u on u.id = m.user_id
where m.tenant_id = $1
order by u.email;

-- name: InsertRefreshToken :exec
insert into refresh_tokens (user_id, tenant_id, family_id, token_hash, expires_at)
values ($1, $2, $3, $4, $5);

-- name: GetRefreshTokenForUpdate :one
select id, user_id, tenant_id, family_id, expires_at, used_at, revoked_at
from refresh_tokens where token_hash = $1
for update;

-- name: MarkRefreshTokenUsed :exec
update refresh_tokens set used_at = now() where id = $1;

-- name: RevokeFamily :execrows
update refresh_tokens set revoked_at = now() where family_id = $1 and revoked_at is null;

-- name: DeleteExpiredRefreshTokens :execrows
delete from refresh_tokens where expires_at < now() - interval '7 days';

-- name: GetTenant :one
-- Runs in a tenant transaction: RLS only lets the scoped tenant through.
select id, name, cuit from tenants where id = $1;

-- name: LockTenant :one
-- Serializes membership changes that must keep at least one owner.
select id from tenants where id = $1 for update;

-- name: CountOwners :one
select count(*) from memberships where tenant_id = $1 and role = 'owner';

-- name: DeleteMembership :one
delete from memberships where user_id = $1 and tenant_id = $2 returning role;

-- name: SetPasswordIfUnset :execrows
-- Never overwrites a password the person already chose.
update users set password_hash = $2 where id = $1 and password_hash is null;

-- name: GetUserPasswordSet :one
select (password_hash is not null)::boolean as has_password from users where id = $1;

-- name: RevokeOpenInvites :execrows
update invites set revoked_at = now()
where user_id = $1 and used_at is null and revoked_at is null;

-- name: InsertInvite :exec
insert into invites (tenant_id, user_id, created_by, token_hash, expires_at)
values ($1, $2, $3, $4, $5);

-- name: GetInviteForUpdate :one
select id, tenant_id, user_id, expires_at, used_at, revoked_at
from invites where token_hash = $1
for update;

-- name: MarkInviteUsed :exec
update invites set used_at = now() where id = $1;

-- name: DeleteExpiredInvites :execrows
delete from invites where expires_at < now() - interval '7 days';

-- name: InsertMagicLink :exec
insert into magic_links (user_id, tenant_id, token_hash, expires_at)
values ($1, $2, $3, $4);

-- name: GetMagicLinkForUpdate :one
select id, user_id, tenant_id, expires_at, used_at
from magic_links where token_hash = $1
for update;

-- name: MarkMagicLinkUsed :exec
update magic_links set used_at = now() where id = $1;

-- name: DeleteExpiredMagicLinks :execrows
delete from magic_links where expires_at < now() - interval '7 days';

-- name: RevokeUserTenantSessions :execrows
update refresh_tokens set revoked_at = now()
where user_id = $1 and tenant_id = $2 and revoked_at is null;
