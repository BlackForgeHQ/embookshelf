// Package dberr centralizes the error-inspection helpers that repos used
// to do inline against pgx-specific types. Today only the Postgres branch
// is implemented; the SQLite branch lands in Plan 2 alongside the SQLite
// driver wiring.
package dberr

import (
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsNotFound reports whether err denotes "no rows" from any supported
// driver. Today that's database/sql; pgx-native errors are no longer
// returned to callers because repos use *sql.DB.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

// IsUniqueViolation reports whether err denotes a unique-constraint
// violation, and if so returns the violated constraint's name (or "" if
// the driver doesn't expose it). Callers use the constraint name to
// distinguish e.g. ErrLibraryNameTaken from ErrLibraryPathTaken.
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
