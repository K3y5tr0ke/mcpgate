package postgres

import "embed"

// Migrations holds the embedded SQL migration files.
// golang-migrate and testutil/db.go both use this FS.
//
//go:embed migrations/*.sql
var Migrations embed.FS
