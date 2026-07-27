// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// arrayMap decodes text[] literals. pgtype.Map is safe for concurrent use
// once built and carries no per-connection state for the default types, so
// one package-level instance serves every scan.
var arrayMap = pgtype.NewMap()

// TextArray is the scan destination for a Postgres text[] column. Wrap the
// target field — `rows.Scan(db.TextArray{Dst: &b.Genres})` — instead of
// scanning into an `any` and converting afterwards.
//
// The wrapper exists because pgx's stdlib driver does not hand database/sql
// a []string for text[]: `stdlib.Rows.Next` has typed branches for bool,
// bytea, the numeric OIDs, json/jsonb and the timestamp OIDs, and everything
// else — arrays included — falls through to a `string` branch that yields the
// raw array literal. database/sql cannot assign a string to a *[]string, so
// some adapter has to sit in between. This one delegates the parsing to pgx's
// own ArrayCodec rather than to a hand-written literal parser.
//
// A SQL NULL yields a nil slice; an empty array yields an empty non-nil
// slice, matching what the column round-trips. A NULL *element* is an error
// rather than an empty string: []string cannot represent one, and no write
// path in embookshelf produces one.
type TextArray struct {
	Dst *[]string
}

// Scan implements sql.Scanner.
func (a TextArray) Scan(src any) error {
	if a.Dst == nil {
		return fmt.Errorf("scan text[]: nil dst")
	}
	if src == nil {
		*a.Dst = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case string:
		raw = []byte(v)
	case []byte:
		raw = v
	default:
		return fmt.Errorf("scan text[]: unexpected type %T", src)
	}
	if err := arrayMap.Scan(pgtype.TextArrayOID, pgtype.TextFormatCode, raw, a.Dst); err != nil {
		return fmt.Errorf("scan text[]: %w", err)
	}
	return nil
}

// TextArray is only ever a scan destination, never a bound argument — repos
// pass []string straight through on the write side and pgx encodes it.
var _ sql.Scanner = TextArray{}
