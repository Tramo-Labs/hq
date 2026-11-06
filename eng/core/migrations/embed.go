// Package migrations embeds the goose SQL migrations so every binary and
// every test applies exactly the files in this directory.
//
// Numbering: 00001 to 00099 platform and identity, 00100 to 00199 ledger,
// documents and matching. A migration is never edited once on main.
package migrations

import "embed"

// FS holds the *.sql migrations.
//
//go:embed *.sql
var FS embed.FS
