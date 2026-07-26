// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dberr centralizes the error-inspection helpers that repos used
// to do inline against pgx-specific types, so repos don't import driver
// packages.
//
// Postgres is the only supported database (ADR-0023). These helpers used
// to carry SQLite fallbacks that matched on driver message text and
// translated column tuples back to Postgres constraint names; boot now
// refuses a SQLite DSN, so nothing could reach them. The importer reads
// SQLite but writes Postgres, and does its own SQLSTATE check.
package dberr

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsNotFound reports whether err denotes "no rows".
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

// IsForeignKeyViolation reports whether err denotes a foreign-key
// constraint violation (SQLSTATE 23503).
func IsForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

// IsUniqueViolation reports whether err denotes a unique-constraint
// violation (SQLSTATE 23505), and if so returns the violated
// constraint's name — e.g. `libraries_slug_key`. Repo code branches on
// that name to tell one uniqueness rule from another (CreateLibrary
// distinguishing a duplicate slug from a duplicate path).
func IsUniqueViolation(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true, pgErr.ConstraintName
	}
	return false, ""
}
