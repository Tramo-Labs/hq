# inbox

The client's own mailbox for statements (D-22, ADR 0009). Each tenant has
one address with a random code (`<26 base32 chars>@INBOX_DOMAIN`) that the
owner can rotate. Mail is accepted only from senders the owner authorized
and whose domain authenticates (SPF or DKIM aligned with the From); its
csv, txt, xls, xlsx and pdf attachments go to datos' ingestion, which
routes them by bank and account number, quarantines and dedupes. The body,
the subject and the other headers are never stored. Every message leaves a
receipt the portal shows. Owned by backend.

**Status.** Built: addresses, senders, receipts, `Receive` with every
check, the API. Not yet: the receiving provider (Martín chooses: own server
or Mailgun EU; `INBOX_DOMAIN` stays empty until then and addresses are not
shown), and ingestion without an account (datos-5, `Ingest`): until it
exists every accepted attachment is `not_delivered` and nothing is stored.

## Receiving

An adapter per provider turns a mail into a `Message` (To, From,
Message-ID, the provider's own SPF and DKIM alignment verdicts, the
received time, attachments opened lazily) and calls `Service.Receive`.
Checks, in order, each refusal stopping the rest:

1. The address: the local part is looked up with `db.InboxTenant` (role
   `tramo_inbox`, below). Wrong domain, unknown or rotated code:
   `ErrUnknownAddress`, nothing stored.
2. A retry of a message already received (same Message-ID, kept only as a
   SHA-256): its receipt again, nothing ingested twice.
3. The sender authorized, then SPF or DKIM aligned, then how many
   attachments. Refused here, no attachment is read at all.
4. Each attachment read through a limit: past 20 MiB it is cut at the
   limit plus one byte and refused `too_large` (the ledger's own limit, so
   a larger one never reaches it); the message's total past 25 MiB refuses
   the whole message. The kind needs both the extension and the first
   bytes (`%PDF-`, the zip header for xlsx, the OLE header for xls, text
   without control characters for csv and txt); anything else is refused
   `unsupported_kind`.
5. Under a per tenant advisory lock: the day's limits (Argentina's day),
   then each accepted attachment to `Ingest`, then the receipt.

## Limits (one place: `DefaultLimits` in `limits.go`, shown by `GET /v1/inbox`)

| Limit | Value |
|---|---|
| Attachments per message | 10 |
| Bytes per attachment | 20 MiB |
| Bytes per message | 25 MiB |
| Accepted messages per tenant per day | 200 |
| Accepted messages per sender per day | 50 |
| Refused messages that still leave a receipt per day | 50 (past it they are dropped, so a stranger flooding the address fills nothing and spends none of the client's quota) |

Not measured: agreed with lead-4 as room for a client's statements.

## Endpoints (api/openapi.yaml, tag `inbox`)

| Method and path | Who | Idempotency-Key | What it does |
|---|---|---|---|
| `GET /v1/inbox` | owner, accountant, operator | | Address (null before one exists or with no domain configured), authorized senders, limits. |
| `POST /v1/inbox/address` | owner | required | Creates the address or replaces it; the old one stops at once. |
| `POST /v1/inbox/senders` | owner | required | Authorizes a sender (one plain address, lowercased); 409 `already_authorized`. |
| `DELETE /v1/inbox/senders/{id}` | owner | | Stops accepting mail from it; 404 if not the tenant's. |
| `GET /v1/inbox/receipts` | owner, accountant, operator | | The latest 200 receipts: sender, status, reason, per attachment name, size, hash, status and what ingestion did. |

Roles are read from `memberships` in the transaction, not from the token.

## Tables (migration 00201)

| Table | Holds | Access |
|---|---|---|
| `inbox.addresses` | code, who created it, when, revoked_at. One active per tenant. | `tramo_app` with RLS by tenant |
| `inbox.senders` | authorized emails, who added them, removed_at | `tramo_app` with RLS by tenant |
| `inbox.receipts` | per message: Message-ID hash, received at, Argentine day, sender, status, reason, attachments (jsonb) | `tramo_app` select and insert, RLS by tenant |

`tramo_inbox`: no login, no table privileges; it may only execute
`inbox.address_tenant(code)`, a security definer function that returns the
tenant of an active address and nothing else. `db.InboxTenant` runs that
one fixed query and takes no callback (ADR 0002 and 0009).

## Jobs

None.

## Sensitive data

Statements carry third parties' personal data (Ley 25.326): they pass
through memory to ingestion and are not stored here. Receipts keep the
sender's address (the owner authorized it) and file names; never the
subject, the body or other headers. The ingestion detail kept in a receipt
is the tenant's own, like an upload's, and is never logged. Every receipt,
rotation and sender change is audited (no content in the audit detail).

## How it is tested

`go test ./internal/inbox/` (embedded Postgres): the lookup role holds no
privilege on any table in the database and may execute only
`inbox.address_tenant` (checked over the catalog, and by reading tenants,
users, memberships, the inbox tables, the audit log and closes as it,
each denied); a delivered message with good, foreign and disguised
attachments; refusals for an unauthorized sender, no alignment and too many
attachments read zero bytes; a 500 MiB attachment is read only to its
limit and the rest of the message goes on; a message over its total is
refused having read at most the total; a retry is the same receipt; no
ingestion means `not_delivered`; wrong domain, unknown code, rotated
address and removed sender; the daily limits under eight concurrent
deliveries (exactly 3 accepted, 2 refused, 3 dropped); the per sender
limit; only the owner changes the mailbox; isolation between tenants.
`internal/app/inbox_test.go` covers the HTTP contract and that a receipt
has no subject, body or headers.

Mutations checked: granting `tramo_inbox` select on one table fails the
role test; without the receive lock the concurrency test fails (3 of 3
runs); reading attachments without the limit fails the size test by
assertion after about 21 MB: the test's reader counts every byte and
refuses to give more than the limits allow plus a small margin, so memory
stays bounded even when the code under test is wrong.

## Deploy

`tramo_inbox` is created by migration 00201 itself (no login, no
password, like `tramo_jobs`), run by the privileged migration URL, which
also grants it to `tramo_runtime` when that role exists. The production
init script creates `tramo_runtime` before the first migration, so nothing
is added to Terraform or compose. A `tramo_runtime` created after 00201
ran needs `grant tramo_inbox to tramo_runtime` by hand.
`TestRuntimeLoginRoleReachesDataOnlyThroughTheRoleSwitch` checks the
lookup works as `tramo_runtime`.
