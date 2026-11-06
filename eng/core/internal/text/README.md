# text

Rules for short texts people type that others will read. One place, so
every module refuses the same invisible input.

- `Invisible(r)`: the one list of characters that change how text reads
  without showing themselves: controls, format characters (zero width, soft
  hyphen, bidi controls), line and paragraph separators, and the blank
  looking letters below. Identity refuses them in emails (F30); `Name` uses
  it. No module keeps its own list (`identity.TestIdentityUsesTheSharedTextRules`
  checks every code point).
- `Space(r)`: a space of any kind (ASCII and Unicode white space and every
  space separator: NBSP, thin, narrow, ideographic, ogham). Identity
  refuses it inside an email, with `Invisible` (F30: `owner\u00a0@alfa.test`
  gave 201).
- `HasLetterOrNumber(s)`: at least one letter or number that shows. Spaces
  of any kind, zero width and bidi characters, and the letters that look
  blank (Hangul fillers U+3164, U+115F, U+1160, U+FFA0, Braille blank
  U+2800) do not count. Matching uses it for every required note (reject,
  other, resolve, return): a note of only U+200B is no reason (F28).
- `Name(s, max)`: trims and refuses a name with nothing left, more than
  `max` characters, invalid UTF-8, control or format characters (zero width,
  bidi controls such as U+202E), line or paragraph separators, blank looking
  letters, or no letter or number. Identity applies it to a person's and a
  company's name, ledger to an account's.

No endpoints, tables, jobs or events. No data is stored here.

Tests: `go test ./internal/text/`. The invisible characters are written as
escapes in the tests so a review can see them.
