# text

Rules for short texts people type that others will read. One place, so
every module refuses the same invisible input.

- `HasLetterOrNumber(s)`: at least one letter or number that shows. Spaces
  of any kind, zero width and bidi characters, and the letters that look
  blank (Hangul fillers U+3164, U+115F, U+1160, U+FFA0, Braille blank
  U+2800) do not count. Matching uses it for every required note (reject,
  other, resolve, return): a note of only U+200B is no reason (F28).
- `Name(s, max)`: trims and refuses a name with nothing left, more than
  `max` characters, invalid UTF-8, control or format characters (zero width,
  bidi controls such as U+202E), line or paragraph separators, blank looking
  letters, or no letter or number. It is the rule identity applies to a
  person's name (`identity/name.go`, not exported); identity can move to it.

No endpoints, tables, jobs or events. No data is stored here.

Tests: `go test ./internal/text/`. The invisible characters are written as
escapes in the tests so a review can see them.
