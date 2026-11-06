# documents

Supplier invoices in, fields out, each with its source and its confidence.
Sources are tried in a fixed order and a later source never overwrites an
earlier one:

1. **ARCA QR** (RG 4291, spec in `eng/arca/manuales/qr.txt`). Found in a
   link annotation, in the PDF text, in a Flate or JPEG image inside the PDF,
   or in a photo. Validated field by field: CUIT check digit, 14 digit CAE,
   total with at most two decimals, known `tipoCodAut`, version 1. A QR that
   fails any check is not a source. Two different valid QRs in one document
   and none is used. Confidence 0.98: the QR says what the issuer declared;
   WSCDC (through the gateway) is what confirms ARCA authorized it.
2. **Text layer of the PDF**, by rules over labels ("Punto de Venta",
   "Comp. Nro", "Fecha de Emisión", "CUIT", "Importe Total", "CAE N°", "COD.").
   The tenant's own CUIT is excluded from the issuer candidates.
3. **OCR**, only when the PDF has no text layer.
4. **Language model**, last, only for the fields still missing.

When a later source reads a different value for a field already filled, the
field keeps the first value and records the other as a `Conflict`. A field
in conflict is not trusted until a person resolves it. A model value never
creates a conflict: a model disagreeing with the QR is noise.

## Confidence

| Case | Confidence |
|------|-----------|
| Valid QR | 0.98 |
| Text: label found, value validates, one distinct value | 0.90 |
| Text: type derived from kind and letter ("FACTURA A") | 0.86 |
| Text: same label with different values | 0.50 |
| OCR: the text rules, one level lower | 0.75 / 0.71 / 0.35 |
| Language model (cap) | 0.60 |

`AutoThreshold` is 0.85. An invoice is `AutoEligible` only when every
required field (issuer CUIT, type, point of sale, number, date, total, CAE)
is at or above it, from a source other than the model, and not in conflict.
Matching receives `MinConfidence` and keeps anything below the threshold out
of the automatic tray.

**These numbers are rule levels, not measured precision.** They get replaced
by measured precision per source once there is a set of real invoices
labeled by a person. Until then no precision figure is published.

## OCR and language model

Both are interfaces (`OCR`, `LLM`) with fakes in the tests. **No real
implementation that sends documents to an outside provider is added without
Martín deciding it**: it is a third party processing client data, and it
changes the data processing addendum with each client (`ops/LEGAL.md`). A
local OCR (running on our own machine) is a separate decision too.

## Not done yet

- WSCDC constatation of each invoice: the gateway does it (task 3); the
  invoice will carry the result.
- QR inside images with other filters (JBIG2, CCITT) or color spaces
  (indexed): the text and link paths still apply.
- Line items and VAT breakdown.

## Open items from invoices (F2)

Every stored invoice generates the open item matching crosses against, in
the same transaction (`DraftItem`, stored by matching through
`PGStore.OnSaved`): a receivable when the tenant issued it (positive), a
payable when the tenant received it (negative); credit notes reverse the
sign. Amount is the total, date the issue date, refs the point of sale and
number ("0002-00001234" and "00001234"), party the other side's CUIT.

- **Direction is certain only from the QR**: the QR issuer is the tenant,
  or the QR receiver is the tenant. The text rules leave the tenant's own
  CUIT out of the issuer candidates, so an invoice the tenant issued and
  that has no QR looks received. An uncertain direction caps the item's
  confidence at 0.6: it can only be suggested, never automatic (tested).
- The item's confidence is the invoice's `MinConfidence`: only an invoice
  that is `auto_eligible` with a certain direction reaches automatic.
- **An uncertain direction makes the invoice itself not `auto_eligible`**
  (migration 00107, `direction_uncertain`). Decided so the invoice and its
  item never say two different things: before, a text-only invoice of the
  tenant was `auto_eligible` (fields at 0.9) while its item was capped in
  Suggested, and it did not count in "para revisar". Now it counts there,
  and its trail says why (`TrailDirectionUncertain`). The other option,
  keeping it eligible and only flagging the doubt, was turned down:
  `auto_eligible` is what the panel and the filters read, and a flag next
  to it would need every reader to combine both.
- No item, with the reason in `item_reason` (and the invoice listed to
  review, `review=true`): no tenant CUIT on file, no trusted total or issue
  date, a currency other than pesos (no conversion is guessed), no issuer.
- The item follows its invoice (deleted with it). An item a match in effect
  settles cannot change or be deleted, also through its invoice: the
  database refuses it until the match is reverted (migration 00104).

Not done: open items from the ERP or from a CSV of expected payments.

## Endpoints, tables, jobs, events

Table (migration 00101): `documents.invoices`, RLS enabled and forced.
Every field with its source and confidence as JSON, the trail, and
`auto_eligible`. The key columns (issuer, type, point of sale, number, date,
total) are filled only from trusted fields: the same invoice in a second
file is `ErrDuplicateInvoice`, the same file is `ErrSameDocument`, and an
uncertain reading is never called a duplicate. `PGStore` uses
`db.InTenant` only; audit action `documents.invoice_stored`.

Endpoints (tag `documents`): `POST /v1/documents/invoices` (multipart
`file`, PDF or image up to 15 MiB; every role but viewer; 201 with every
field, its source, confidence and whether it is trusted; 409 for the same
file or the same invoice; 400 for bytes that are neither a PDF nor an
image) and `GET /v1/documents/invoices/{invoiceId}`. The tenant's own CUIT
is read from the tenant and excluded from the issuer candidates.

## Sensitive data

Invoices carry CUITs, names, amounts and CAEs of third parties. Stored
encrypted at rest; never logged. The trail records stages and counts, not
values.

## Tests

`go test ./internal/documents/`. Every PDF and QR is generated at test time
by a minimal PDF writer (`pdfgen_test.go`); no binary fixture and no real
invoice is committed. The CUITs used have valid check digits and an all zero
body.

- ARCA's own example from the specification parses to the expected fields.
- Malformed QRs are rejected; URL safe base64 and `+` survive.
- The QR is found in a Flate image, a JPEG image, a link, and a photo.
- Text only, two CUITs without the tenant's CUIT, text in conflict with the
  QR, code and letter in disagreement, type from the letter alone.
- Scans go to OCR and stay below the threshold; the model is asked only for
  missing fields, its invalid values are dropped and it never qualifies an
  invoice for automatic; malformed PDFs fail without a panic.
