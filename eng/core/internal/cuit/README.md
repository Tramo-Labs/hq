# cuit

Validates Argentine CUIT and CUIL numbers: 11 digits whose last one is a
mod 11 check digit over the first ten, with weights 5 4 3 2 7 6 5 4 3 2. A
result of 11 maps to 0; a result of 10 is never issued and is rejected.
Owned by backend. Used by identity (a tenant's CUIT) and by the ARCA
gateway and its configuration.

- `Parse(s)`: an 11 digit string, no dashes, to its number, or
  `ErrInvalid`.
- `Valid(n)`: whether a number is a well formed CUIT.

It checks the shape only: a valid check digit does not mean the CUIT
exists or belongs to whoever sent it (that is ARCA's padrón, not this
package).

## Endpoints, tables, jobs

None.

## Sensitive data

A CUIT identifies a company or a person; this package only computes over
it and never logs or stores it.

## How it is tested

`go test ./internal/cuit/`: `TestParse` and `TestValid` over well formed
numbers, wrong check digits, wrong lengths, non digits and out of range
values.
