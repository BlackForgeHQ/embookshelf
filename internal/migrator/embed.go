package migrator

import "embed"

//go:embed migrations/*.sql
var FS embed.FS

// Subpath is the directory inside FS that holds the migration files.
const Subpath = "migrations"
