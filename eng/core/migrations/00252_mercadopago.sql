-- +goose Up
-- Mercado Pago connected by OAuth with PKCE (D-22, ADR 0010). One
-- connection per tenant with its tokens encrypted by the tenant's data key
-- (platform envelope, purpose "mercadopago.token"), and the pending
-- authorization requests (state and PKCE verifier) of the last minutes.
create schema mercadopago;
grant usage on schema mercadopago to tramo_app;

create table mercadopago.connections (
  tenant_id        uuid primary key references tenants(id) on delete cascade,
  status           text not null check (status in ('connected', 'needs_reconnect')),
  mp_user_id       text,
  access_token     bytea,          -- envelope blob, never plaintext
  refresh_token    bytea,          -- envelope blob, never plaintext
  token_expires_at timestamptz,
  scope            text,
  connected_by     uuid references users(id) on delete set null,
  connected_at     timestamptz not null default now(),
  last_refresh_at  timestamptz,
  last_error_code  text check (last_error_code in ('revoked', 'mp_unavailable')),
  last_error_at    timestamptz,
  -- A connection waiting for reconnect keeps no token.
  check (status = 'connected' or (access_token is null and refresh_token is null)),
  check (status = 'needs_reconnect' or (access_token is not null and token_expires_at is not null))
);

grant select, insert, update, delete on mercadopago.connections to tramo_app;
alter table mercadopago.connections enable row level security;
create policy connections_tenant on mercadopago.connections to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table mercadopago.connections force row level security;

-- state_hash: SHA-256 of the state sent to Mercado Pago (the state itself
-- is only in the browser's URL). verifier: the PKCE code verifier, sealed.
-- Ten minutes, single use; who started it must be who finishes it.
create table mercadopago.auth_requests (
  state_hash  bytea primary key,
  tenant_id   uuid not null references tenants(id) on delete cascade,
  user_id     uuid not null references users(id) on delete cascade,
  verifier    bytea not null,
  created_at  timestamptz not null default now(),
  expires_at  timestamptz not null,
  used_at     timestamptz
);
create index auth_requests_tenant on mercadopago.auth_requests (tenant_id, created_at);

grant select, insert, update, delete on mercadopago.auth_requests to tramo_app;
alter table mercadopago.auth_requests enable row level security;
create policy auth_requests_tenant on mercadopago.auth_requests to tramo_app
  using (tenant_id = app_current_tenant()) with check (tenant_id = app_current_tenant());
alter table mercadopago.auth_requests force row level security;

-- +goose Down
drop table mercadopago.auth_requests;
drop table mercadopago.connections;
drop schema mercadopago;
