# identity

Accounts, tenants, memberships and sessions. Owned by backend.

## Endpoints (api/openapi.yaml, tag `identity`)

| Method and path | Auth | Idempotency-Key | What it does |
|-----------------|------|-----------------|--------------|
| `POST /v1/auth/register` | none | required | Creates user, tenant, owner membership and the tenant data key. Returns ids only. |
| `POST /v1/auth/login` | none | no (returns tokens, stores nothing replayable) | Email and password to a 10 minute access token plus a 30 day refresh token. |
| `POST /v1/auth/invite/accept` | invitation token | no (creates nothing; like login) | Accepts an invitation: the pending membership becomes active and a session opens on that tenant. A person without a password sets one here. |
| `POST /v1/auth/magic-link` | none | required | Emails a one time login link (15 minutes) if the email has an account. Always 202, so it does not reveal which emails exist. |
| `POST /v1/auth/magic-link/accept` | link token | no (creates nothing; like login) | Opens a session with the link, on the tenant asked for or the oldest membership. |
| `POST /v1/auth/refresh` | refresh token | no | Rotates the refresh token. Reuse of an old one revokes the whole family. |
| `POST /v1/auth/switch-tenant` | refresh token | no (returns tokens, like refresh) | Moves the session to another of the caller's tenants, same family, no password. |
| `POST /v1/auth/logout` | refresh token | no | Revokes the family and clears the cookie. |
| `GET /v1/me` | bearer | | The caller (with their name, null until set), active tenant and role, every membership. |
| `PATCH /v1/me` | bearer, any role, only oneself | no (changes, creates nothing) | Sets the caller's own name: trimmed, 1 to 120 characters, no control characters (400 `invalid_name`). |
| `GET /v1/tenant/members` | bearer | | Members of the active tenant. |
| `POST /v1/tenant/members` | bearer, owner or operator | required | Invites a person under the grant rules below; the member stays `pending` until they accept (D-19). |
| `DELETE /v1/tenant/members/{userId}` | bearer, owner | | Removes a person, active or pending (204). 404 if neither, 409 `last_owner` for the last owner. |

Errors (problem `code`): `unauthorized`, `refresh_token_reused`, `origin_not_allowed`,
`magic_link_required`, `invalid_magic_link`, `invalid_name`, `invalid_tenant_name` (the company name, same rule as a person's name, F27), `email_taken`,
`slug_taken`, `cuit_taken`, `already_member`, `invalid_email`, `invalid_cuit`,
`forbidden`, `not_a_member`, plus the platform codes.

`not_a_member` (403, "not a member of that tenant") answers a login with the
right password and a `tenantId` the user does not belong to. A wrong
password on any tenant stays the generic 401, so it says nothing to someone
without the password, and it is not counted as a failed guess
(`TestLoginIntoAForeignTenantSaysNotAMember`; mutation checked: answering
the generic 401 again fails it).

## Who grants which role

Spec §1 and §2 (`CanGrant`):

| Granter | May grant |
|---------|-----------|
| owner | owner, approver, viewer, accountant |
| operator | owner, and only while the tenant has no owner |
| approver, viewer, accountant | nothing |

Nobody grants `operator` over HTTP. Anything else is 403 (and audited as
`authz.denied`). The operator's "no owner yet" check runs again inside the
insert's transaction with the tenant row locked, so two operators racing
create one owner, not two.

**The first operator** comes only from the command line, run where the api
runs:

```
printf '%s\n' "$PASSWORD" | api create-operator -email ops@example.com -tenant <uuid> -password-stdin
```

It needs only `DATABASE_URL`, reads the password from the first line of
stdin (never a flag or the environment), creates the user when missing,
never overwrites an existing password, refuses a person who already has
another role in that tenant, and is idempotent: a second run changes
nothing. Audited as `identity.operator_created` (or
`identity.password_set`) with request id `cli-<uuid>`, no user and no IP.

**Not built** (separate item): the operator creating a tenant (spec §2,
step 1). Today `register` makes the registering person the owner, so the
operator's "first owner" grant applies only to a tenant left without one.

## Invitations (F8, F26, D-19)

Adding a person is **always an invitation**, for a new email and for an
existing account alike (D-19). The membership waits in
`pending_memberships` until the person accepts: everything that grants
access, fills the switcher (`/v1/me`) or counts owners reads `memberships`,
so a pending row gives nothing by construction, and nothing in the person's
account is written (F25). `GET /v1/tenant/members` lists pending people with
`status: pending` and `name: null`: the inviting tenant does not see their
account. Adding someone already pending replaces the role and mails a fresh
invitation; adding an active member is 409 `already_member`.

- Token: 256 random bits, base64url (43 characters). Only its SHA-256 is
  stored, in `invites`, with the person, the tenant, who invited and the
  expiry: **72 hours, single use**. A new invitation from the same tenant
  revokes that tenant's earlier unused ones; another tenant's stay valid.
- Accepting checks everything before writing anything, in one transaction:
  the token, then the pending membership. If it was removed, the answer is
  403 `not_a_member` and no password is saved (F29,
  `TestRemovedInviteeCannotSetAPassword` and `TestRemovingAPendingMember`);
  adding them again sends a new invitation.
- A person without a password sets one when accepting (400
  `password_required` without it, and the link stays usable). A person with
  an account just accepts: a password sent along is ignored, theirs is never
  changed (`TestAddingAnExistingAccountIsAnInvitation`). The name given is
  kept only if the person has none.
- An invitation mailed before D-19 came with an active membership; it still
  works once for a person without a password.
- The token goes **only into the email**: never in the API response, a log
  or the audit log. Handing it to the inviter would let one tenant's owner
  set the password of a person who also belongs to another tenant.
- The link is `APP_BASE_URL/invitacion#token=...`: the token rides in the
  URL fragment, which browsers never send to a server, so it does not land
  in access logs or Referer headers. The web app reads it and calls
  `POST /v1/auth/invite/accept`.
- Accepting locks the invitation row and the pending membership, marks the
  invitation used, moves the pending row into `memberships` and opens a
  session on that tenant. Unknown, used, revoked and expired tokens all
  answer the same 401 `invalid_invite`. 10 attempts per IP per 15 minutes,
  checked before any hashing (429 `rate_limited`).
- Audit: `identity.invite_created` (by the inviter, with the role),
  `identity.invite_accepted`, `identity.invite_rejected` (with the reason:
  unknown, used, revoked, expired, no membership, password required,
  password already set). Adding is `identity.member_invited`; removing a
  pending person, `identity.member_removed` with `status: pending`.
- **Delivery.** The only mail transport is a local outbox file
  (`MAIL_OUTBOX`, created and kept at mode 0600). There is **no SMTP**: no
  email leaves the machine. `make dev` writes to `DEV_DATA_DIR/outbox`.
  Without `MAIL_OUTBOX` the invitation is created but not delivered, and
  that is logged. A real transport is a separate item.
- **Copy.** Invitation and magic link emails are in Spanish with voseo
  (F31, `TestEmailsAreInSpanish`), roles named as the portal names them
  (titular, aprobador, lectura, contador, operador). Backend wrote the
  text; producto owns it from here.

## Removing a member

Only an owner removes people (spec §1), any role, the operator included:
the data is the client's, and so is the decision of who reaches it,
Tramo included (lead's decision, 06/11). An owner may remove themself while
another owner remains; the last owner can not be removed (409
`last_owner`). Removing a pending person deletes the pending row, so their
invitation answers 403 from then on. The tenant row is locked during the removal, so two owners
removing each other at the same time leave exactly one.

**When access ends.** The removed person's refresh tokens for that tenant
are revoked at once, so the session can not be renewed and adding the
person back later does not revive it. An access token already issued keeps
working until it expires: **up to 10 minutes** after the removal. Audited
as `identity.member_removed` with the removed role.

## Sessions

- **Access token**: JWT signed with EdDSA (ed25519 from `JWT_SIGNING_KEY`),
  `kid` from `JWT_KEY_ID`, 10 minutes, claims `sub` (user), `tid` (tenant),
  `role`, `iss`, `aud`, `jti`. Only EdDSA is accepted (no `none`, no HMAC).
  Never stored. A removed membership stops working at the next refresh
  (at most 10 minutes later).
- **Refresh token**: 256 random bits, base64url. Only its SHA-256 is stored,
  with user, tenant, family and expiry. Every refresh marks the token used
  and issues a new one in the same family. Presenting a used token revokes
  every token of the family and answers `refresh_token_reused`: the
  legitimate client and the thief both have to log in again.
- **Delivery**: `tokenDelivery: cookie` (default, browsers) puts the refresh
  token only in `tramo_refresh`, `HttpOnly; Secure; SameSite=Strict;
  Path=/v1/auth`. `body` (API clients) returns it in the JSON.
- **Origin check (F22, ADR 0007)**: the cookie alone authorizes refresh and
  logout, so a request to either that carries `tramo_refresh` must have an
  Origin on the CORS list (`CORS_ALLOWED_ORIGINS`). A missing or foreign
  Origin is 403 `origin_not_allowed`, checked before the handler: the token
  is neither rotated nor revoked. A token in the body, without the cookie,
  needs no Origin (`TestRefreshAndLogoutByCookieNeedAnAllowedOrigin`;
  mutations checked: letting a missing Origin through, and leaving logout
  out, each fail it). The check is not audited: these calls have no tenant
  scope yet. Switching tenant follows the same rule.
- **Switching company (no password)**: `POST /v1/auth/switch-tenant` takes
  the refresh token, not the access token, so a stolen 10 minute access
  token cannot open sessions in other companies. The token rotates in the
  **same family**, now bound to the target tenant with that membership's
  role; replaying an old one revokes the family as on refresh. A tenant the
  caller is not in is 403 `not_a_member` and the token stays usable.
  Audited as `identity.tenant_switched` in the target tenant, with from and
  to. It is how someone who signed in by magic link changes company
  (`TestSwitchTenantWithoutPassword`,
  `TestSwitchTenantByCookieNeedsAnAllowedOrigin`; mutations checked: the
  refused switch consuming the token, a new family on switch, no Origin
  check, the role of the old tenant, not audited, each fails).
- **Passwords**: argon2id, RFC 9106 second recommendation (64 MiB, t=3, p=4),
  PHC string with its parameters so they can be raised later. Verification is
  constant time and bounds the parameters a stored hash may demand. Unknown
  emails are checked against a dummy hash so timing and message do not reveal
  which emails exist at login. Registration does reveal it (`email_taken`).
- **Roles** per tenant: `owner`, `approver`, `viewer`, `operator`,
  `accountant`. A user may belong to several tenants; the token carries one.

## Tables

`users`, `tenants`, `memberships`, `refresh_tokens` (migrations 00001 to
00003), `invites` (00110), `magic_links` (00111) and `magic_link_requests` (00114), all only through
`tramo_system`; `tramo_app` has no access. `pending_memberships` (00116)
has `tenant_id` and RLS: `tramo_app` reads and writes the active tenant's
rows, and keeps the email copied at invitation time, since it can not see
a user row before the person belongs to the tenant. Queries in `queries.sql`, Go generated by sqlc into `store/`.
Registration, login, refresh, logout and `me` run as `tramo_system` (no tenant
yet, or across the caller's own memberships); member listing and adding run
as `tramo_app` under RLS.

## Jobs

`PurgeExpiredRefreshTokens`, `PurgeExpiredInvites` and
`PurgeExpiredMagicLinks` remove refresh tokens, invitations and magic links
expired more than 7 days ago. All run in the worker's daily
`platform.maintenance` job.

## Sensitive data

- Password hashes: argon2id only, not readable by `tramo_app`.
- The person's name (`users.display_name`, 00113): personal data, set at
  registration, on accepting an invitation (only if none yet) or with
  `PATCH /v1/me`, only by the person. Visible in `/me` and to the members
  of the same tenant, accountant and operator included, as the email is
  (column grant to `tramo_app`); not in any export. `identity.name_changed`
  is audited without the value (`TestPersonName`,
  `TestInvitedPersonGivesTheirName`; mutations checked: not trimmed,
  control characters allowed, no column grant, the value audited, `/me`
  without it, the invitation ignoring it, each fails). A name refuses
  format characters (Cf: zero width, the bidi controls such as U+202E that
  flip how it reads in the audit or the members list) and line or paragraph
  separators, and characters that look blank but are letters or symbols to
  Unicode (the Hangul fillers U+3164, U+115F, U+1160, U+FFA0 and the Braille
  blank U+2800), anywhere in the name. It needs at least one letter or
  number: punctuation, symbols or emoji alone ("...", "★") are not a work
  name. The 120 character limit is enforced in code too
  (`TestNameLengthBoundary`, `TestNameRefusesInvisibleAndBidiCharacters`).
  An invitation never overwrites a name already set
  (`TestInviteDoesNotOverwriteAName`). Mutations checked: the invitation
  overwriting, the limit off by one, format characters allowed, no visible
  character needed, each fails.
- Refresh tokens: only SHA-256 stored; not readable by `tramo_app`.
- Invitation and magic link tokens: only SHA-256 stored; the clear token
  exists only in the email (the outbox file in dev and tests), in the URL
  fragment so it never reaches a server log. Never in a response.
- Asking for a magic link answers 202 for any email; known or not, it
  counts against the same send limits. The request never looks the email
  up: it checks the throttles and enqueues one `identity.magic_link` job,
  the same for both, so neither the answer nor its timing tells whether an
  account exists. The job (worker) looks it up, stores the link and mails
  it, or does nothing (`TestAskingDoesTheSameWorkForKnownAndUnknownEmails`:
  no outbox write and no link row during the request, one identical job
  per request but the email; mutations checked: sending in the request, and
  skipping the queue for unknown emails, each fail a test). The email, IP
  and request id wait in `magic_link_requests` (00114, `tramo_system`
  only), stored for every request alike; the job carries only that row's
  id, never personal data (River keeps job arguments in clear), and takes
  (deletes) the row in the transaction that stores the link. Rows an
  enqueue left behind are purged after a day (`TestMagicLinkJobSendsThroughTheQueue`
  reads `river.river_job`; `TestStaleMagicLinkRequestsArePurged`; mutations
  checked: the email back in the arguments, the row not taken, nothing
  stored for unknown emails, no purge, each fails).
- The signing key lives only in the environment. Rotating it invalidates
  access tokens (10 minutes) but not refresh tokens.
- Audit (`audit_log`): `identity.register`, `identity.login`,
  `identity.login_failed`, `identity.refresh_reuse`, `identity.logout`,
  `identity.member_added`, `identity.member_removed`, `identity.magic_link_sent`,
  `identity.magic_link_login`, `identity.magic_link_rejected`,
  `identity.magic_link_mode`, with request id and IP. Never passwords or tokens.
  Every 403 of any module also leaves `authz.denied` (platform README).
- No clave fiscal and no home banking credential has a field anywhere: the
  request schemas reject unknown properties.

## Throttling (login and registration)

Checked before any password hashing, so a flood costs no argon2 time.
Answer: 429 `rate_limited` with `Retry-After` (seconds).

D-17: login is limited per IP and per email and IP pair, **never per email
alone**. Nobody can lock the owner out of her account from anywhere.

| Throttle | Default | Stops |
|----------|---------|-------|
| login requests per IP | 30 per minute | spraying many emails from one machine |
| failed logins per email and IP | 5 per 15 minutes | guessing one person's password |
| 20 failed logins per email from 3 or more IPs in 15 minutes | magic link mode for 1 hour (not a 429) | distributed guessing |
| registrations per IP | 10 per hour | sign up spam |
| magic link requests per IP | 10 per 15 minutes | link spam from one machine |
| magic links sent per email, any IP | 3 per 15 minutes | mail bombing one person |
| magic link acceptances per IP | 10 per 15 minutes | bounds anonymous work |

A success (password or magic link) clears that IP's failures for the email.
The pair's 429 carries `magicLink: true`: the web offers "send me a link",
so even the owner throttled on her own IP gets in. The per IP 429 does not
carry it (that one is about spraying, not about one person).

**Magic link mode.** Twenty failures on one email from at least three
distinct IPs within 15 minutes switch the email to magic link for an hour.
In that mode the password is not checked at all: right or wrong, login
answers the same 401 `magic_link_required`, so the mode is not a password
oracle and the attacker's guesses stop being tested. The owner gets in with
the link (`POST /v1/auth/magic-link`), which goes to her email only. The
switch is audited once (`identity.magic_link_mode`, user id only, nothing
for an email without an account). Accepting a link does not lift the mode
for others. An email in the mode is never evicted when the watch is full,
so flooding it with other emails cannot lift it either.

**Trade off, deliberate:** an attacker with three or more IPs can force the
owner to sign in with the link for an hour. That is a nuisance, not a
lockout: she always gets in. Martín decides whether he wants other
thresholds (`DefaultLimits` in `throttle.go`).

In production the browser calls api.X directly (D-18) and the client IP is
what our Caddy sees; `TRUSTED_PROXIES` is only Caddy. The web app's same
origin `/v1` proxy is for dev only; if it is used, it must be listed and
forward the browser's address, or every person shares its per IP counters
(platform README).

Limits: counters are in memory, per api process (with several replicas the
limits multiply; restart clears them). The client IP is the TCP peer, or,
when the peer is a configured trusted proxy (`TRUSTED_PROXIES`), the first
`X-Forwarded-For` hop from the right that is not one of ours. A direct
client rotating `X-Forwarded-For` still counts as its real IP.
The 429 response is declared in `api/openapi.yaml` for login and register,
with `Retry-After`.

## Not built yet

TOTP (required for approvers of payments), API keys for integrations.

## How it is tested

- `unit_test.go`: argon2id format, salts, wrong password, tampered cost
  parameters; JWT expiry at 10 minutes, another key, another `kid`, HS256
  and `none` algorithm confusion; CUIT check digit including the 11 to 0
  case; refresh tokens are 256 bit and stored hashed.
- `internal/app/identity_test.go`, end to end over HTTP with an embedded
  Postgres: registration replay, 422 on another body, validation and
  conflicts; login failures look the same for unknown and wrong; cookie
  attributes; rotation and **reuse revokes the family** (checked by
  mutation: removing the revocation fails the test); refresh by cookie and
  logout; **a tenant does not see another tenant's members**; roles
  (viewer cannot add, owner cannot grant operator); a removed membership
  ends the session at refresh; audit rows with request id and IP and no
  password; throttling: the 6th guess from one IP gets 429 even with the
  right password while the owner still logs in from her IP, distributed
  guessing on one email switches it to magic link (401, not 429), one IP spraying 40 emails gets 10
  refusals, the 11th registration from one IP is refused (mutations checked:
  without the login check, and with the failure counter not keyed by IP,
  the test fails).
- Magic link and D-17 (`internal/app/magiclink_test.go`,
  `throttle_test.go`): twenty failures from twenty IPs answer
  `magic_link_required` for the right and the wrong password alike, the
  owner gets in with the link, the link works once, an unknown link is
  refused, another email is untouched; thirty failures from two IPs never
  switch the email, nineteen from five do not, the twentieth does; the
  pair's 429 carries `magicLink` and the per IP one does not; a link
  accepted from the throttled IP clears the pair; asking answers 202 for
  known and unknown emails alike and mails only the known one, needs an
  Idempotency-Key, three sends per email and ten per IP; a link expires at
  15 minutes; the window is 15 minutes and the mode one hour (fake clock);
  the watch is bounded and a flood of other emails does not lift a mode.
  Mutations checked, each fails a test: the password checked in the mode,
  the mode as a 429, distinct IPs ignored, `magicLink` on every 429 and on
  none, the link not marked used, expiry not checked, the pair not cleared
  on accept, no send limit per email, the window never reset, a mode
  evicted under a flood.
- `internal/app/members_test.go`: `TestRemoveMember` (approver, viewer,
  accountant and operator get 403; the owner removes; the refresh dies and
  stays dead after adding the person back; the access token still answers
  right after; 404 on a second removal; 409 for the last owner; the owner
  removes the operator; audit rows) and
  `TestRemovingOwnersConcurrentlyKeepsOne` (10 rounds of two owners
  removing each other). Mutations checked: without the tenant lock, without
  the last owner check, letting the operator remove, and without the
  revocation, each fails.
- `internal/app/authz_test.go`: `TestGrantRules` (every granter against
  every role on a tenant with an owner, then on one without: the operator
  grants the first owner and not a second, nor any other role) and
  `TestOperatorsRacingForTheFirstOwner` (10 rounds of two operators).
  `cmd/api/operator_test.go`: `TestCreateOperatorCommand` (password only
  from stdin, length checked, idempotent, password not overwritten, audit
  row with `cli-` request id, another role not promoted, unknown tenant
  refused). Mutations checked: the old rules, an operator granting any
  role, no locked check in the insert's transaction, the owner count
  ignored, the audit action changed, another role accepted silently: each
  fails.
- `internal/app/invite_test.go`: `TestInvitationLetsANewPersonIn` (token
  only in the mail, not in the response; outbox 0600; link in the
  fragment; no login before accepting; accept opens the inviting tenant
  with the granted role; second use 401; login works after; no invitation
  for a person with a password; audit without the token) and
  `TestInvitationsThatCanNotBeUsed` (expired, superseded by a later
  invitation, marked used while still passwordless, unknown: same 401;
  the newest one works; two concurrent accepts give one 200; the 11th
  attempt from one IP is 429; no token in clear). Mutations checked: no
  expiry check, earlier invitations not revoked, no throttle, no used
  check, and no used check plus no password guard: each fails.
  `internal/platform/mail`: outbox mode forced to 0600, header injection
  refused.
- `internal/platform/ratelimit`: windows, expiry, reset, bounded memory,
  exactly 100 of 400 concurrent events allowed.
