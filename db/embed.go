// Package db embeds the goose SQL migrations into the binary so the server can
// migrate its own schema on startup with no external files to ship.
package db

import "embed"

// Migrations holds the goose .sql migration files. goose.SetBaseFS points at
// this FS and runs the "migrations" directory.
//
//go:embed migrations/*.sql
var Migrations embed.FS
