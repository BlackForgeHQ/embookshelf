// Package dberr centralizes the error-inspection helpers that repos used
// to do inline against pgx-specific types. Branches for Postgres (pgx)
// and SQLite (modernc.org/sqlite) live behind one interface so repos
// don't import driver packages.
package dberr

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsNotFound reports whether err denotes "no rows" from any supported
// driver. Today that's database/sql (sql.ErrNoRows); the underlying
// driver is irrelevant because repos use *sql.DB.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

// IsForeignKeyViolation reports whether err denotes a foreign-key
// constraint violation. On Postgres that is SQLSTATE 23503; on SQLite
// the driver emits a message containing "FOREIGN KEY constraint failed".
func IsForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}

// IsUniqueViolation reports whether err denotes a unique-constraint
// violation, and if so returns a stable identifier for the violated
// constraint.
//
// On Postgres the identifier is the constraint name (`libraries_slug_key`)
// taken from *pgconn.PgError.ConstraintName.
//
// On SQLite the underlying error reports the columns that violated the
// uniqueness instead. We translate them to the equivalent PG constraint
// name using sqliteUniqueIndex so callers can compare against the same
// string regardless of backend. Unknown column-tuples return the raw
// dotted form ("table.column" or "table.col_a, table.col_b") so they
// surface in logs and the caller can decide what to do.
func IsUniqueViolation(err error) (bool, string) {
	if err == nil {
		return false, ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true, pgErr.ConstraintName
	}
	if msg := err.Error(); strings.Contains(msg, "UNIQUE constraint failed:") {
		// Pull the substring after the marker.
		i := strings.Index(msg, "UNIQUE constraint failed:")
		tail := strings.TrimSpace(msg[i+len("UNIQUE constraint failed:"):])
		// Strip any trailing " (2067)" or similar appended by some wrappers.
		if j := strings.Index(tail, " ("); j != -1 {
			tail = tail[:j]
		}
		// Normalize whitespace in the column list.
		cols := strings.Join(strings.Fields(tail), " ")
		if name, ok := sqliteUniqueIndex[cols]; ok {
			return true, name
		}
		return true, cols
	}
	return false, ""
}

// sqliteUniqueIndex maps SQLite's column-tuple form of a violated
// uniqueness constraint to the equivalent Postgres constraint name.
// Keep this in sync with the unique indexes declared in the SQLite
// squashed init (internal/migrator/migrations/sqlite/0000_init.up.sql).
//
// Add an entry whenever a new unique index lands. Repo code that
// branches on a constraint name (e.g. CreateLibrary distinguishing
// slug vs path) must continue to receive the PG-flavored name on
// either backend.
var sqliteUniqueIndex = map[string]string{
	"libraries.slug":                          "libraries_slug_key",
	"libraries.path":                          "libraries_path_key",
	"users.email":                             "users_email_key",
	"shelves.user_id, shelves.slug":           "shelves_user_id_slug_key",
	"bookdrop_items.path":                     "bookdrop_items_path_key",
	"user_devices.user_id, user_devices.name": "idx_user_devices_user_name",
	"app_settings.name":                       "app_settings_pkey",
	"provider_settings.id":                    "provider_settings_pkey",
}
