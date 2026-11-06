# ledger

Bank and Mercado Pago exports in, trusted movements out. A file enters whole
or not at all: if any check fails it goes to quarantine with a reason, and
nothing of it reaches matching.

## The checks, in order

What the file is comes before whether it was seen: the layout, the rows and
the bank and account are checked first, so the same bytes sent to an
account of another bank are `bank_mismatch`, not `duplicate` (F9). Then:
the **file hash** (SHA-256 of the bytes): the same file already accepted
for the tenant is `duplicate` and creates nothing (Duotach case 4). A
quarantined file does not burn its hash: once its layout is fixed it can be
uploaded again. Then balances, chain and movement keys.

1. **Layout fingerprint.** The first header-like row (three or more cells,
   none a date or an amount) is normalized (lowercase, no accents, single
   spaces, trailing empty cells dropped) and hashed with the delimiter. Any
   added, removed, renamed or reordered column changes it. An unknown
   fingerprint is `unknown_layout` and the detail carries the new
   fingerprint, never the header contents.
2. **Bank and account.** The layout's bank has to be the account's bank; when
   the file states an account number it has to match the registered one.
3. **Rows.** Every row has to parse: date, amounts in the layout's decimal
   style (see `internal/money`), exactly one of debit and credit. One bad row
   stops the file (`row_parse`, with line and column).
4. **Balances.** Opening plus credits minus debits equals closing; when rows
   carry a running balance, each one equals the previous plus the movement;
   a stated closing equals the last running balance. A file with no balance
   evidence at all is refused. A layout that always states its balances
   (Santander trailer, Mercado Pago total row) without them is `incomplete`
   (Duotach case 6).
5. **Chain.** When a statement starts after the previous one of the account
   ends, its opening has to equal the previous closing (`chain_gap`).
   Overlapping exports skip this check: deduplication handles them.
6. **Movement keys.** SHA-256 of account, date, amount, running balance,
   references (the bank voucher and the holder's reference, when the file
   has them) and the occurrence number among identical movements of the
   same file. The same movement in two overlapping exports gets the same
   key and is stored once; two genuine identical transfers on the same day
   get different keys.
7. **Description against the stored one (F3).** The description is not part
   of the key: a bank that rewrites a concept between two exports, or an
   edited cell, must not turn one movement into two. When a key is already
   stored, the normalized description has to be the same (case, accents
   and spacing do not count); otherwise the whole file is
   `movement_conflict`, with the line, and nothing of it enters. The stored
   movement is never overwritten. In doubt, quarantine: never money counted
   twice.

   Known limit: with no running balance and no voucher, two movements of
   the same day and amount are told apart only by their order in the file.
   If a later export lists them in another order, the file is quarantined
   (a false alarm an operator clears), never stored twice. Keys changed
   format with F3: a file stored before it would not be recognized again,
   which is fine while there is no client data.

## Layouts

| ID | Bank | Format | Balances |
|----|------|--------|----------|
| `galicia/csv-v1` | Galicia | `;`, Latin-1, newest first | running |
| `santander/csv-v1` | Santander | `;`, preamble and trailer | stated and running |
| `bancor/csv-v1` | Bancor | `,`, quoted amounts | running |
| `macro/txt-v1` | Macro | tab, `dd/mm/yy`, signed amount | running |
| `mercadopago/release-v1` | Mercado Pago | `,`, release report, decimal dot | stated (initial and total rows) |

**None of these is verified against a real export yet** (`Layout.Verified`
is false for all). The bank layouts were written from synthetic samples
because the export menus could not be inspected; Mercado Pago follows the
documented release report (record types `initial_available_balance`,
`release`, `total`) but whether the `total` row includes the initial balance
needs a real report. The first real file of each bank either fits, passes
the balance check and the layout is marked verified, or it lands in
quarantine and the layout is fixed from it. In both cases nothing wrong
enters.

Known limit: for layouts with only a running balance, a file cut at the end
still closes on its own (the last row is the closing). The chain check
catches it when the next statement arrives.

For Mercado Pago, each release with a gross amount has to satisfy gross plus
documented fees equals net, or the file is refused. Those fees travel with
the movement so matching can explain the difference without inventing it.

## Endpoints, tables, jobs, events

Tables (migration 00100, schema `ledger`): `accounts`, `files` (accepted
or quarantined, with reason and detail, and since 00108 `uploaded_by`, the
user who uploaded it; null before that), `statements`, `movements`. Every
table has RLS enabled and forced with the platform tenant policy, and
references its parents by `(tenant_id, id)` so no row can point at another
tenant's. Unique: accepted file per tenant and hash; movement per account
and key.

`PGStore` implements `Store` on `db.InTenant` only. Each accepted file is
one transaction (file, statement, movements, audit entry). Audit actions:
`ledger.account_created`, `ledger.statement_accepted`,
`ledger.file_quarantined` (the quarantine notice the operator console
reads). `MemStore` stays as the reference behavior for the domain tests.

Endpoints (`api/openapi.yaml`, tag `ledger`):

| Method and path | Who | Answer |
|---|---|---|
| `GET /v1/ledger/accounts` | any member | accounts of the tenant |
| `POST /v1/ledger/accounts` | owner, operator | 201 account |
| `POST /v1/ledger/accounts/{accountId}/statements` | owner, approver, operator, accountant | 201 accepted or quarantined, 200 duplicate; multipart `file`, up to 20 MiB |
| `GET /v1/ledger/quarantine` | any member | quarantined files, newest first |
| `GET /v1/ledger/files?period=&status=` | any member | uploaded files, accepted or quarantined, newest first (F12): when, who uploaded it (`uploadedBy`, and the email while that person is still a member), account, statement days, movements it stored, reason when quarantined. `period` is the month it was received (Argentina time). Up to 500, `truncated` says there were more |
| `GET /v1/ledger/movements?period=&accountId=` | any member | the stored movements dated in the month (`period` required), with the file and line each came from. Up to 5000, `truncated` says there were more |

Every POST requires `Idempotency-Key`. A retry with the same key replays the
first answer only when the body bytes are the same: a client that rebuilds
the multipart body (new boundary) gets 422 and should send a new key, which
then answers `duplicate`.

## Sensitive data

Bank statements: account numbers, counterparties' names, amounts. Files are
stored encrypted at rest with the tenant data key (platform `crypto`).
Quarantine details and logs carry reason codes, line numbers and
fingerprints, never cell contents, names or amounts.

## Tests

`go test ./internal/ledger/`. Fixtures in `testdata/` are synthetic and
generated by `testdata/gen_fixtures.py` (invented names and account numbers).

- The five layouts parse, close and come out in chronological order.
- Latin-1 input, Mercado Pago gross and fees.
- Quarantine for each reason: changed layout, truncated file, broken running
  balance (line named), bad amount, fee that does not add up, missing total,
  wrong bank, wrong account.
- Re-upload is a duplicate; overlapping exports store the shared day once;
  twins stay two; the chain check catches a gap; keys are stable.
- An edited description (formula prefixes, another concept) is
  `movement_conflict` naming the line, with nothing from the file in the
  detail; a change of case or spacing enters with nothing new; the
  matching metrics still count the month's movements once (F3).
