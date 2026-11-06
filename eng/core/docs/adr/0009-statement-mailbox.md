# 0009: Statement mailbox: a lookup role, a provider behind an interface, no body kept

Date: 2026-11-06. Status: accepted.

Context: D-22: statements come in by Mercado Pago, by a mailbox of the
client's own, or uploaded by hand. The design (backend-3, approved by
lead-2 and lead-4) asks for an address with a random code the owner
rotates, mail only from senders the owner authorized and whose domain
authenticates, csv, txt, xls, xlsx and pdf attachments to datos'
ingestion, never the body, limits per message, day and sender, a receipt
in the portal, and the receiving provider behind an interface until
Martín chooses (own server or Mailgun EU). Ley 25.326: statements carry
personal data of third parties.

Decisions:

1. **A role for one lookup, not `tramo_system`.** Mail arrives before any
   tenant scope. `tramo_system` reaches identity tables; it would let a
   bug in mail handling read them. `tramo_inbox` instead has no table
   privileges and may execute one security definer function,
   `inbox.address_tenant(code)`, owned by the migration owner, with a
   fixed search path. `db.InboxTenant` runs that single fixed query; it
   takes no callback. Everything after it runs under the found tenant's
   scope (`tramo_app`, RLS), with no user, like a job.
2. **The provider is an adapter that builds a `Message`.** `Receive`
   takes it; the adapter reports SPF and DKIM alignment from its own
   verification, never from headers the sender wrote. Attachments are
   opened lazily and read through a limit, so an oversized one is cut at
   20 MiB plus one byte and a message at 25 MiB plus one; the adapter
   should stream from the provider for the same reason.
3. **Cheap refusals first.** An unauthorized or unauthenticated sender,
   or too many attachments, is refused before any attachment is read.
4. **Nothing of the content but the attachments.** Receipts keep the
   sender (the owner authorized it), the Message-ID only as a SHA-256 (to
   answer a retry with the same receipt instead of ingesting twice), and
   per attachment its name, size, hash and what ingestion did. Never the
   subject, the body or other headers.
5. **Limits in one place** (`DefaultLimits`), shown to the client by the
   API. The day's limits are checked under a per tenant advisory lock, so
   concurrent deliveries cannot pass them together. Refused messages leave
   a receipt up to their own daily cap and are dropped past it, so a
   stranger flooding the address neither fills the table nor uses up the
   client's accepted quota.
6. **Ingestion is datos's**, through `Ingest.IngestUnassigned`: routing by
   bank and number, quarantine and dedupe are not repeated here. Until it
   exists, attachments are `not_delivered` and nothing is stored.

7. **The migration creates `tramo_inbox`**, like `tramo_jobs` (00007):
   no login, no password, so it needs nothing from the operator. It is
   granted to `tramo_runtime` when that role exists, which in production
   it does (the init script creates it before the first migration). No
   change to Terraform or compose.

Deploy checklist: `inbox.address_tenant` runs with its owner's rights,
and its owner is whoever ran migration 00201. In tests that is the
`postgres` superuser, so RLS never applies to it there. In production it
must be the migration owner, which is not a superuser (ADR 0005); the
function then reads `inbox.addresses` only through the `owner_lookup`
policy that 00201 creates for that same role. After the first deploy,
check `select pg_get_userbyid(proowner), r.rolsuper from pg_proc p join
pg_roles r on r.oid = p.proowner where proname = 'address_tenant'` shows
the migration owner and `false`, and that a mail to a known address is
found (one receipt appears).

Consequences: switching providers touches only the adapter. Rotating the
address is the answer to a leaked one; the old address drops mail at once
and leaves no receipt (there is no tenant to write it under).
