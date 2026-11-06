# money

Amounts as integer cents (`money.Amount`, an `int64`) and one strict parser.
Used by ledger, documents and matching; backend uses it too instead of its own.

## Why

Floats lose cents, and a lenient parser turns a bank export with a changed
decimal separator into wrong amounts that still add up somewhere. `Parse`
takes the decimal style of the source explicitly and rejects anything that
does not fit it.

## API

- `Parse(s, DecimalComma)` reads `1.234.567,89`, `$ 1.234,5`, `-1.234,56`,
  `1.234,56-` and `(1.234,56)`.
- `Parse(s, DecimalDot)` reads `1234567.89` and `1,234.56`.
- Rejected: more than two decimals, a thousands group that is not three
  digits, the other style's decimal separator, stray characters, two signs,
  more than 15 integer digits.
- `Amount.String()` renders `1234.56` (dot, no grouping). It is the canonical
  form stored in text fields (documents) and compared across sources.

## Endpoints, tables, jobs, events

None.

## Sensitive data

None by itself. Amounts are business data: they never go to logs (see
`eng/ARQUITECTURA.md` section 6).

## Tests

`go test ./internal/money/`: accepted and rejected forms of both styles.
