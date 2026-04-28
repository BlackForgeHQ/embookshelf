package db

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// SelectQ returns the dialect-appropriate SQL string. Used pervasively in
// repos when a query needs a different shape on Postgres vs SQLite.
//
// If a query is identical between dialects (e.g. simple lookups by id),
// callers should use just one constant and pass it as both args; the cost
// is negligible.
func SelectQ(d Dialect, pg, sqlite string) string {
	if d == DialectSQLite {
		return sqlite
	}
	return pg
}

// NewID returns a fresh canonical UUID string. Both Postgres (UUID column)
// and SQLite (TEXT column) accept the 36-char hyphenated form. Repos call
// this for every INSERT instead of relying on Postgres' gen_random_uuid()
// default, so the same INSERT shape works on both backends.
func NewID() string {
	return uuid.NewString()
}

// ValueStringSlice prepares a []string for binding into a query as a
// dialect-appropriate value.
//
//   - Postgres: returns the slice unchanged. The pgx stdlib codec
//     (registered when we open the *sql.DB via stdlib.OpenDBFromPool)
//     encodes []string as a TEXT[] literal automatically.
//   - SQLite: returns a JSON-encoded string. The repo's INSERT/UPDATE
//     should bind into a TEXT column with a CHECK (json_valid(col)).
func ValueStringSlice(d Dialect, s []string) (any, error) {
	if d == DialectPostgres {
		return s, nil
	}
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode string slice: %w", err)
	}
	return string(b), nil
}

// ScanStringSlice decodes a value retrieved from the database into *[]string.
//
//   - Postgres: the source is already a []string courtesy of the pgx codec.
//     We copy it into dst.
//   - SQLite: the source is a string (or []byte) containing JSON. We
//     json.Unmarshal it.
//
// Nil source produces an empty slice; this matches both Postgres' empty
// array literal '{}' and SQLite's default '[]'.
func ScanStringSlice(d Dialect, src any, dst *[]string) error {
	if dst == nil {
		return fmt.Errorf("scan string slice: nil dst")
	}
	if src == nil {
		*dst = nil
		return nil
	}
	if d == DialectPostgres {
		s, ok := src.([]string)
		if !ok {
			return fmt.Errorf("scan string slice (PG): unexpected type %T", src)
		}
		*dst = append((*dst)[:0], s...)
		return nil
	}
	// SQLite
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("scan string slice (SQLite): unexpected type %T", src)
	}
	return json.Unmarshal(b, dst)
}
