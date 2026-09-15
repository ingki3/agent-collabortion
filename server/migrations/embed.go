// Package migrations holds the SQL schema migrations for the Colab server
// (PLAN.md §3 P0-a "스키마 v0"). Files are named NNNN_name.sql and applied in
// numeric order by server/internal/db.
//
// Rule: a migration that has been applied anywhere is never edited — add a new
// file instead. The runner records a checksum and refuses to start if an
// applied file changed.
package migrations

import "embed"

// FS contains every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
