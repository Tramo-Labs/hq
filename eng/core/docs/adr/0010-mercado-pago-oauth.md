# 0010: Mercado Pago by OAuth with PKCE, tokens sealed per tenant

Date: 2026-11-06. Status: accepted.

Context: D-22: a client's movements come in by Mercado Pago through OAuth,
never by sharing a password and never by scraping. The design was approved
by lead-4. Martín creates the app in Mercado Pago Developers and chooses
the return domain; until then everything is configured by environment and
connecting answers 501.

What Mercado Pago documents, read on 2026-11-06 (quoted facts only; where
a page is silent, it says so here):

- Authorization URL `https://auth.mercadopago.com/authorization` with
  `response_type=code`, `client_id`, `redirect_uri`, `platform_id=mp`,
  `state`, and for PKCE `code_challenge` and `code_challenge_method`
  (S256 or plain). PKCE becomes mandatory once enabled in the app's
  settings; the verifier is 43 to 128 characters. The redirect URI must be
  the static one registered in the app.
  https://www.mercadopago.com.br/developers/pt/docs/security/oauth/creation
  (the prose there names the field "code_method"; its URL example and the
  RFC say `code_challenge_method`, which is what we send).
- Token endpoint `POST https://api.mercadopago.com/oauth/token`, JSON body:
  `client_id`, `client_secret`, `grant_type` (`authorization_code` with
  `code`, `redirect_uri` and `code_verifier`; or `refresh_token` with
  `refresh_token`). The answer carries `access_token`, `token_type`,
  `expires_in`, `scope`, `user_id`, `refresh_token`, `public_key`,
  `live_mode`.
  https://www.mercadopago.com.br/developers/en/reference/authentication/oauth/_oauth_token/post
- Lifetimes: the access token lasts 180 days (15552000 seconds) by
  default; the authorization code 10 minutes, single use. The refresh
  token's own lifetime is not stated. A refresh token is only issued when
  the app has the `offline_access` scope.
  Same reference page, and
  https://www.mercadopago.com.br/developers/en/docs/security/oauth/renewal
- Rotation: every refresh returns a new refresh token, which must be
  stored again. Whether the old one keeps working is not stated; we never
  reuse it. Same renewal page.
- Errors: an invalid, expired or revoked code or refresh token answers 400
  `invalid_grant`; bad client credentials 400 `invalid_client`; 429 for
  rate limits. The error body's exact shape is not shown. Reference page.
- Revocation: **no documented endpoint lets an integrator revoke a token
  or unlink the app.** Authorizations end when the seller revokes them,
  changes their password, on a fraud reset or session cleanup, or when the
  app is deleted; all tokens of that authorization are deleted. Mercado
  Pago can notify authorize and deauthorize events by webhook (configured
  in the developer dashboard; topic and payload not documented there).
  https://www.mercadopago.com.ar/developers/en/docs/security/oauth/management

Decisions:

1. **The return goes to the portal, not to the core.** `MP_REDIRECT_URI`
   is the portal's `/app/mercadopago/vuelta`; the page posts code and state
   to `POST /v1/integrations/mercadopago/connect` with the owner's session.
   So the core checks that the state belongs to the same tenant and the
   same person, with no lookup before a tenant scope and no new role.
2. **State and verifier.** The state is 256 random bits, stored only as a
   SHA-256, valid 10 minutes (like Mercado Pago's code), single use. The
   PKCE verifier (86 characters) is sealed with the tenant's data key. The
   state row is locked while the code is exchanged, so the same state
   cannot be spent twice; a refusal (code rejected, Mercado Pago down)
   does not spend it.
3. **Tokens sealed per tenant** with the platform envelope (purpose
   `mercadopago.token`), never in logs, job arguments or answers.
4. **Renewal**: a daily job renews tokens that expire within 30 days, one
   renewal per tenant at a time (row lock), keeping the stored refresh
   token if an answer carried none. An access token without a refresh token
   (no `offline_access`) cannot be renewed; at expiry the connection asks
   for a reconnect.
5. **Revoked from Mercado Pago**: `invalid_grant` on renewal deletes the
   tokens and sets `needs_reconnect` with error code `revoked`; nothing is
   retried until the owner connects again. Other failures keep the
   connection and record `mp_unavailable`; the next run retries. Only
   codes are kept, never Mercado Pago's text.
6. **Disconnect deletes the tokens here** and answers
   `revokeInMercadoPago: true`, because there is no revoke endpoint to
   call: the portal asks the owner to remove the app in their Mercado Pago
   account too. Using the deauthorize webhook to learn of a revocation
   sooner is left for when the app exists (it needs the dashboard).
7. **A fake Mercado Pago** (`mpfake`) checks credentials, the code's single
   use and 10 minutes, the redirect URI and the S256 challenge, rotates
   refresh tokens, and can revoke a seller or fail with 429 or 5xx. Tests
   and make dev use it; nothing in production can point at it unless
   `MP_API_URL` says so.
8. **Fetching the settlement report** for datos is the next step, on the
   interface agreed with datos-5 (the report as a file through the same
   ingestion as the mailbox if it comes as CSV); a file in quarantine stops
   the sync without retry loops until an operator resolves it.

**Open, not to be coded until answered.** Datos-5 chose the "Liberaciones"
(released money) report because its rows carry a balance (BALANCE_AMOUNT)
and a settlement date (DATE); "Todas las transacciones" has no balance.
Pages read on 2026-11-06, under
https://www.mercadopago.com.ar/developers/es/docs/checkout-api-orders/resources/reports/
(released-money/introduction, generate, report-fields; account-money/...;
activities-reports/api). Caveat: the tool returned those pages partly
summarized, not verbatim, so these are leads until read live. What they
do not say: (a) the API endpoints for the released money report (only
`/v1/reporting/operations/{reportId}/...` is documented, for the
"activities" reports, and released money's reportId is not listed); and
(b) whether those endpoints accept a connected seller's OAuth token, acting
on the seller's behalf, rather than the app's own production token. (b)
decides the whole design. To be settled by a single sandbox call once
Martín creates the app, or by asking Mercado Pago's integrator support.
`lastImportAt` stays absent until then.

Consequences: Martín's app must enable PKCE and `offline_access`, and
register the portal's return page. Without `offline_access` every client
reconnects every 180 days.
