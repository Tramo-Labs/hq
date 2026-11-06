# mercadopago

A client's Mercado Pago account connected by OAuth with PKCE (D-22, ADR
0010, which cites Mercado Pago's pages). The owner authorizes; the
portal's return page hands back code and state; the tokens are kept sealed
with the tenant's data key and renewed before they expire. Owned by
backend.

**Status.** Built: authorize, connect, status, disconnect, renewal, the
fake. Not yet: the app in Mercado Pago Developers and its return domain
(Martín; until then `MP_*` are empty and connecting answers 501), and
fetching the settlement report for datos (next commit, on the interface
agreed with datos-5).

## Flow

1. `POST /v1/integrations/mercadopago/authorize` (owner): a 256 bit state
   (stored as SHA-256) and a PKCE verifier (sealed), 10 minutes, single
   use. Answers the Mercado Pago URL with `code_challenge` (S256).
2. The seller authorizes in Mercado Pago, which returns the browser to
   `MP_REDIRECT_URI`: the portal's **`/app/mercadopago/vuelta`**, with
   `code` and `state` in its query.
3. The page posts them to `POST /v1/integrations/mercadopago/connect` with
   the owner's session. The state must be this tenant's and this person's,
   unused and not expired; the code is exchanged with the verifier while
   the state row is locked; the tokens are sealed and stored.

Errors of connect, for the return page: 400 `state_invalid`, 403
`state_not_yours`, 409 `state_expired`, 409 `state_used`, 422
`mp_code_rejected`, 502 `mp_unavailable`, 501 `not_configured`. A rejected
code or an outage does not spend the state: it stays usable for a new try
within its 10 minutes.

## Endpoints (api/openapi.yaml, tag `mercadopago`)

| Method and path | Who | Idempotency-Key | What it does |
|---|---|---|---|
| `GET /v1/integrations/mercadopago` | owner, accountant, operator | | `configured`, status (`disconnected`, `connected`, `needs_reconnect`), account, dates, last error as a code. Never a token. |
| `POST /v1/integrations/mercadopago/authorize` | owner | required | Starts a connection (above). |
| `POST /v1/integrations/mercadopago/connect` | owner | required | Finishes it. Connecting again replaces the tokens. |
| `DELETE /v1/integrations/mercadopago` | owner | | Deletes the tokens here; `revokeInMercadoPago: true` tells the owner to remove the app in Mercado Pago too (no revoke endpoint exists). |

Roles are read from memberships in the transaction. Without an app every
write answers 501 `not_configured`, after the role check.

## Tables (migration 00202)

| Table | Holds |
|---|---|
| `mercadopago.connections` | one per tenant: status, Mercado Pago user id, access and refresh tokens (sealed blobs), expiry, scope, who and when connected, last renewal, last error code and when |
| `mercadopago.auth_requests` | pending authorizations: state hash, person, sealed verifier, expiry, used at; purged a day after expiring |

Both with forced RLS by tenant for `tramo_app`.

## Jobs

`mercadopago.refresh` (internal/worker): hourly attempt, once per
Argentine day. For every tenant, under its own scope, renews tokens that
expire within 30 days (`RefreshBefore`), one renewal per tenant at a time.
`invalid_grant` (the seller removed the app, changed the password, the
authorization ended) deletes the tokens and sets `needs_reconnect` with
code `revoked`, and nothing more is tried; other failures record
`mp_unavailable` and the next day retries. Does nothing without an app.

## Configuration (environment only)

`MP_CLIENT_ID`, `MP_CLIENT_SECRET` (secret), `MP_REDIRECT_URI` (the
portal's return page, registered in the app). `MP_AUTH_URL` and
`MP_API_URL` default to Mercado Pago's and point at the fake only in tests
and make dev. The app needs PKCE enabled and the `offline_access` scope;
without it there is no refresh token and each client reconnects at expiry.

## Sensitive data

The tokens give access to the client's Mercado Pago account: sealed with
the tenant's data key (purpose `mercadopago.token`), never logged, never
in job arguments, never in an answer. The client secret is only in the
environment. Every authorize, connect, renewal, failed renewal and
disconnect is audited, with codes and the Mercado Pago user id only.

## How it is tested

Against `mpfake`, which checks credentials, the code's single use and 10
minutes, the redirect URI and the S256 challenge, rotates refresh tokens,
and can revoke a seller or fail with 429 or 5xx. `go test
./internal/mercadopago/`: connecting stores tokens that are not in clear;
each refusal of connect (unknown state, another owner, another tenant, a
code Mercado Pago never gave, Mercado Pago down, state used, state
expired) with nothing stored and the state still usable after a rejected
code or an outage; roles, and 501 after the role check; renewal only
within 30 days of expiry, with the refresh token rotated; four renewals at
once make one call to Mercado Pago; an outage keeps the connection with
`mp_unavailable`; a revocation deletes the tokens, sets `needs_reconnect`
and stops calling; no refresh token means reconnect at expiry;
disconnect. `internal/app/mercadopago_test.go` covers the HTTP contract
and that no answer carries a token; `internal/worker/mprefresh_test.go`
the daily job.

Mutations checked: sending the exchange without the PKCE verifier fails
the connect test (the fake rejects it as Mercado Pago would); treating
`invalid_grant` as an outage fails the revocation test.
