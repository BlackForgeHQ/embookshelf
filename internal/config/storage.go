// SPDX-License-Identifier: AGPL-3.0-or-later

package config

// Dialect mirrors db.Dialect to avoid the config↔db↔repo import cycle.
// The db package imports config (for config.Config in db.Open), so config
// cannot import db. Callers convert with string(dbh.Dialect).
type Dialect string

const (
	// DialectPostgres matches db.DialectPostgres.
	DialectPostgres Dialect = "postgres"
	// DialectSQLite matches db.DialectSQLite.
	DialectSQLite Dialect = "sqlite"
)
