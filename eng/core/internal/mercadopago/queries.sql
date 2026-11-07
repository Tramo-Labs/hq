-- Queries of the mercadopago module (sqlc). All under a tenant scope (RLS).

-- name: MemberRole :one
select role::text from memberships where user_id = app_current_user() and tenant_id = app_current_tenant();

-- name: InsertAuthRequest :exec
insert into mercadopago.auth_requests (state_hash, tenant_id, user_id, verifier, expires_at)
values ($1, app_current_tenant(), app_current_user(), $2, $3);

-- name: AuthRequestForUpdate :one
-- Locked, so two connects with the same state cannot both spend it.
select user_id, verifier, expires_at, used_at from mercadopago.auth_requests
 where tenant_id = app_current_tenant() and state_hash = $1
 for update;

-- name: UseAuthRequest :exec
update mercadopago.auth_requests set used_at = now()
 where tenant_id = app_current_tenant() and state_hash = $1;

-- name: PurgeAuthRequests :exec
-- Requests of this tenant expired for over a day.
delete from mercadopago.auth_requests
 where tenant_id = app_current_tenant() and expires_at < now() - interval '1 day';

-- name: Connection :one
select status, mp_user_id, access_token, refresh_token, token_expires_at, scope, connected_by,
       connected_at, last_refresh_at, last_error_code, last_error_at
  from mercadopago.connections where tenant_id = app_current_tenant();

-- name: ConnectionForUpdate :one
select status, refresh_token, token_expires_at from mercadopago.connections
 where tenant_id = app_current_tenant()
 for update;

-- name: UpsertConnection :exec
insert into mercadopago.connections (tenant_id, status, mp_user_id, access_token, refresh_token,
    token_expires_at, scope, connected_by, connected_at, last_refresh_at, last_error_code, last_error_at)
values (app_current_tenant(), 'connected', $1, $2, $3, $4, $5, app_current_user(), now(), null, null, null)
on conflict (tenant_id) do update set status = 'connected', mp_user_id = excluded.mp_user_id,
    access_token = excluded.access_token, refresh_token = excluded.refresh_token,
    token_expires_at = excluded.token_expires_at, scope = excluded.scope,
    connected_by = excluded.connected_by, connected_at = excluded.connected_at,
    last_refresh_at = null, last_error_code = null, last_error_at = null;

-- name: StoreRefreshed :exec
update mercadopago.connections set access_token = $1, refresh_token = $2, token_expires_at = $3,
    scope = $4, last_refresh_at = now(), last_error_code = null, last_error_at = null
 where tenant_id = app_current_tenant() and status = 'connected';

-- name: MarkRevoked :exec
-- Mercado Pago refused the tokens: they are deleted, the owner reconnects.
update mercadopago.connections set status = 'needs_reconnect', access_token = null, refresh_token = null,
    token_expires_at = null, last_error_code = 'revoked', last_error_at = now()
 where tenant_id = app_current_tenant();

-- name: MarkUnavailable :exec
update mercadopago.connections set last_error_code = 'mp_unavailable', last_error_at = now()
 where tenant_id = app_current_tenant();

-- name: DeleteConnection :execrows
delete from mercadopago.connections where tenant_id = app_current_tenant();
