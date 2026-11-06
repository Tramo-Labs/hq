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
   stops the file (`row_parse`, with line and column). So does a line the
   CSV reader cannot split, such as a stray quote (`row_parse` with the line,
   F16).
4. **Balances.** Opening plus credits minus debits equals closing; when rows
   carry a running balance, each one equals the previous plus the movement;
   a stated closing equals the last running balance. A file with no balance
   evidence at all is refused. A layout that always states its balances
   (Santander trailer, Mercado Pago total row) without them is `incomplete`
   (Duotach case 6). The detail names the line (and sheet), its date, both
   amounts and the difference.
5. **Chain.** When a statement starts after the previous one of the account
   ends, its opening has to equal the previous closing (`chain_gap`).
   Overlapping exports skip this check: deduplication handles them. The
   detail says which days the missing movements are in: "movements are
   missing between 10/10/2026 and 02/11/2026: the statement up to 10/10/2026
   closed at X and this file, from 02/11/2026, opens at Y (difference D)".
   Both boundary days are included, since an export can be cut mid-day.
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
| `bancor/xlsx-v1` | Bancor | Excel, one or more sheets | running |
| `macro/txt-v1` | Macro | tab, `dd/mm/yy`, signed amount | running |
| `mercadopago/release-v1` | Mercado Pago | `,`, release report, decimal dot | stated (initial and total rows) |

**None of these is verified against a real export yet** (`Layout.Verified`
is false for all). The bank layouts were written from synthetic samples
because the export menus could not be inspected, and neither bank publishes
its export columns: Bancor's usage guide (Bancón, 01/2022) only says the web
version downloads movements "en formato Excel o en PDF", and the one
description of Galicia's CSV found (six fields: fecha, descripción, origen,
débitos, créditos, saldo) is a third-party importer page. Mercado Pago follows the
documented release report (record types `initial_available_balance`,
`release`, `total`) but whether the `total` row includes the initial balance
needs a real report. The first real file of each bank either fits, passes
the balance check and the layout is marked verified, or it lands in
quarantine and the layout is fixed from it. In both cases nothing wrong
enters.

Known limit: for layouts with only a running balance, a file cut at the end
still closes on its own (the last row is the closing). The chain check
catches it when the next statement arrives.

### Excel workbooks

`.xlsx` is read with the standard library (zip and XML, `xlsx.go`),
streaming each part, never holding one whole. A 20 MiB upload can expand
a thousand times, so a workbook over any of these limits is
`unsupported_format`, with the limit named in the detail:

| Limit | Value | Checked |
|-------|-------|---------|
| zip central directory, entries | 1 MiB, 1,000 | before the zip is opened |
| decompressed size, all entries | 64 MiB | declared sizes before decompressing; bytes read while reading |
| one XML token (tag, text run, comment) | 1 MiB | while reading |
| element nesting | 32 | while reading |
| text in one cell | 128 KiB | while reading |
| sheets | 50 | while reading the workbook part |
| rows with cells, all sheets | 200,000 | while reading |
| cells, all sheets, skipped columns included | 1,000,000 | while reading |
| shared strings, cell formats, number formats | 500,000, 65,536, 4,096 | while reading |

Zip64 and a zip with data before its directory are refused too.
`TestHostileWorkbooks` holds fourteen hostile files of at most 2.1 MiB and
checks which limit stops each one; with `TRAMO_XLSX_MEASURE=1` it logs what
each cost. Measured on 06/11, no race detector, three runs: the worst growth
of resident memory while reading was 72 to 81 MiB (a million cells, stopped
by the cell limit), and a realistic year of 30,000 rows (8.6 MiB of XML)
took 28 to 29 MiB. The whole test, files built one at a time, peaked at
156 MiB (399 MiB under the race detector) in a scope with `MemoryMax=1G`.
The test asserts at most 96 MiB of growth per file. Every sheet is read like a CSV
sheet, with its own fingerprint (delimiter `SheetDelimiter`, so an Excel
layout never shares a fingerprint with the CSV of the same columns). A
cell Excel stores as a number is read as a number whatever the layout's
text style; a number with more than two decimals is `row_parse` (float
noise below a millionth of a cent is tolerated); a number with a date
format becomes the date. Amounts and dates typed as text keep the layout's
style. Then (`workbook.go`), in doubt quarantine:

- sheets with a known header are the data; a sheet with no known header is
  ignored only when no row of it has a date next to an amount (a cover or a
  summary). Otherwise the file is `unknown_layout` (no known sheet, with
  that sheet's fingerprint) or `unsupported_format` (next to known sheets: a
  movement is never left out silently);
- all data sheets have the same layout and the same stated account;
- they are put in date order and must chain: each opens at the balance the
  previous one closed with (`balance_mismatch` naming both sheets, days and
  amounts) and none starts before the previous one ends (one shared day is
  allowed); the result is one statement with the checks above;
- Excel 97-2003 (`.xls`), a zip that is not a workbook, or a cut file is
  `unsupported_format`.

Lines in a workbook are the sheet's row numbers, and details say "sheet 2,
line 14". Sheets are named by position, never by name (a sheet name can
carry the holder's name). Known limit: the movements table keeps the line,
not the sheet, so two movements of different sheets can show the same line.

### Coverage of a period

`Gaps(spans, from, to)` and `PGStore.Coverage(ctx, from, to)` say, per
account, which days of a period the accepted statements do not prove.
Statements that overlap, or where the later one opens at the earlier one's
closing, chain: nothing is missing between them even if some days have no
rows. A break in balances is a gap with `Missing` and the amount; days
before the first statement or after the last one are a gap without
`Missing` (there may have been no movements). The period close reads it.

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
Its SQL lives in `queries.sql`, compiled by sqlc (`make gen`) into
`store/`; nothing in the module writes row mapping by hand.

Endpoints (`api/openapi.yaml`, tag `ledger`):

| Method and path | Who | Answer |
|---|---|---|
| `GET /v1/ledger/accounts` | any member | accounts of the tenant |
| `POST /v1/ledger/accounts` | owner, operator | 201 account; 400 `invalid_name` when the name has no visible letter or number or carries zero width or bidi characters (`text.Name`, the rule of a person's name, F27); stored trimmed |
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
The detail is shown to the tenant; it is never logged. Quarantine details
carry reason codes, line numbers, columns and fingerprints, never names or
descriptions. A balance that does not close also names the two amounts that
disagree: the tenant needs them to fix its own file, and they are its own
data. The detail goes in the upload answer and the files list, and nowhere
else: the audit entry of a quarantine carries only the reason code, and no
log line carries the detail (tested by capturing the request logger and
reading every audit row).

## Tests

`go test ./internal/ledger/`. Fixtures in `testdata/` are synthetic and
generated by `testdata/gen_fixtures.py` and `testdata/gen_xlsx.py` (invented
names and account numbers; the workbooks are reproducible byte for byte).
Real exports never enter the repository: they live outside git and are
only run locally.

- The five layouts parse, close and come out in chronological order.
- Excel: the workbook gives the same movements, balances and keys as the
  CSV (uploading both stores October once); two sheets out of order, one
  typed as text, come out in date order; a balance break between sheets, a
  changed header, a stray sheet of movements, three decimals, Excel 97-2003,
  a zip that is not a workbook, an oversized part and a cut file are each
  quarantined with the reason and the sheet and line.
- The chain gap and the running balance details name the days and the
  difference; `Gaps` covers chained quiet days, overlaps, a balance break,
  late start and early end, a break before the period, unsorted input; the
  Postgres coverage reads only the tenant's statements.
- Latin-1 input, Mercado Pago gross and fees.
- Quarantine for each reason: changed layout, truncated file, broken running
  balance (line named), bad amount, fee that does not add up, missing total,
  wrong bank, wrong account.
- A stray or unclosed quote is `row_parse` naming the line, for a known
  layout or when no header could be read; never an unknown layout with an
  empty fingerprint, and never a cut sheet read as a whole statement (F16).
- Re-upload is a duplicate; overlapping exports store the shared day once;
  twins stay two; the chain check catches a gap; keys are stable.
- An edited description (formula prefixes, another concept) is
  `movement_conflict` naming the line, with nothing from the file in the
  detail; a change of case or spacing enters with nothing new; the
  matching metrics still count the month's movements once (F3).
