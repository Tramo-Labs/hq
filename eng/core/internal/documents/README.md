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

## Endpoints, tables, jobs, events

Not wired yet; goes in the same separate change as ledger's store, on top of
backend's platform. Planned: `documents.invoices` with the fields as JSON,
the trail, `auto_eligible` and a unique key on issuer, type, point of sale
and number (duplicate invoices caught by the database).

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
