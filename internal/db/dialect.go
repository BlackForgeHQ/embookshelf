// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// sqliteTimeFormats lists the TEXT formats SQLite produces for timestamps.
// The schema stores times as strftime('%Y-%m-%dT%H:%M:%fZ','now'), which
// yields RFC3339Nano with milliseconds. We also accept plain RFC3339 for
// values written by older schema revisions or manual inserts.
var sqliteTimeFormats = []string{
	"2006-01-02T15:04:05.999999999Z07:00", // RFC3339Nano
	"2006-01-02T15:04:05Z07:00",           // RFC3339
	"2006-01-02T15:04:05.999999999",       // no TZ suffix (UTC assumed)
	"2006-01-02T15:04:05",                 // no TZ, no sub-seconds
	"2006-01-02 15:04:05",                 // SQLite datetime() format
}

// ScanTime reads a non-nullable timestamp column into *time.Time.
//
//   - Postgres: the pgx codec delivers a time.Time directly; we assert it.
//   - SQLite: the column is TEXT; we parse it through sqliteTimeFormats.
func ScanTime(d Dialect, src any, dst *time.Time) error {
	if dst == nil {
		return fmt.Errorf("scan time: nil dst")
	}
	if src == nil {
		return fmt.Errorf("scan time: unexpected NULL for non-nullable column")
	}
	if d == DialectPostgres {
		t, ok := src.(time.Time)
		if !ok {
			return fmt.Errorf("scan time (PG): unexpected type %T", src)
		}
		*dst = t
		return nil
	}
	// SQLite — value arrives as string
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("scan time (SQLite): unexpected type %T", src)
	}
	for _, layout := range sqliteTimeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			*dst = t.UTC()
			return nil
		}
	}
	return fmt.Errorf("scan time (SQLite): cannot parse %q as time", s)
}

// ScanNullTime reads a nullable timestamp column into **time.Time (pointer
// to pointer). The outer pointer must not be nil; the inner pointer is set
// to nil on SQL NULL and to a parsed value otherwise.
func ScanNullTime(d Dialect, src any, dst **time.Time) error {
	if dst == nil {
		return fmt.Errorf("scan null time: nil dst")
	}
	if src == nil {
		*dst = nil
		return nil
	}
	var t time.Time
	if err := ScanTime(d, src, &t); err != nil {
		return err
	}
	*dst = &t
	return nil
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
		switch v := src.(type) {
		case []string:
			*dst = append((*dst)[:0], v...)
			return nil
		case string:
			parsed, err := parsePGTextArray(v)
			if err != nil {
				return fmt.Errorf("scan string slice (PG): %w", err)
			}
			*dst = parsed
			return nil
		case []byte:
			parsed, err := parsePGTextArray(string(v))
			if err != nil {
				return fmt.Errorf("scan string slice (PG): %w", err)
			}
			*dst = parsed
			return nil
		default:
			return fmt.Errorf("scan string slice (PG): unexpected type %T", src)
		}
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

// parsePGTextArray decodes a PostgreSQL text-array literal (e.g. `{a,b,"c,d"}`)
// into a []string. pgx's stdlib driver hands TEXT[] values to database/sql
// scanners as this literal string when the destination is `any`, so repos
// that scan into `any` see the raw form. NULL elements are mapped to "".
func parsePGTextArray(s string) ([]string, error) {
	if s == "" {
		return []string{}, nil
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("malformed PG array literal: %q", s)
	}
	body := s[1 : len(s)-1]
	if body == "" {
		return []string{}, nil
	}
	out := make([]string, 0, 4)
	var buf strings.Builder
	inQuotes := false
	quoted := false // current element was quoted (so 'NULL' must stay literal)
	flush := func() {
		if !quoted && buf.String() == "NULL" {
			out = append(out, "")
		} else {
			out = append(out, buf.String())
		}
		buf.Reset()
		quoted = false
	}
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inQuotes {
			if c == '\\' && i+1 < len(body) {
				buf.WriteByte(body[i+1])
				i++
				continue
			}
			if c == '"' {
				inQuotes = false
				continue
			}
			buf.WriteByte(c)
			continue
		}
		if c == '"' {
			inQuotes = true
			quoted = true
			continue
		}
		if c == ',' {
			flush()
			continue
		}
		buf.WriteByte(c)
	}
	if inQuotes {
		return nil, fmt.Errorf("unterminated quoted element in PG array: %q", s)
	}
	flush()
	return out, nil
}
