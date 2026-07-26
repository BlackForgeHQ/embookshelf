// SPDX-License-Identifier: AGPL-3.0-or-later

package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// NewID returns a fresh canonical UUID string. Repos call this for every
// INSERT instead of relying on Postgres' gen_random_uuid() default, so the
// generated id is known to the caller without a RETURNING round-trip.
func NewID() string {
	return uuid.NewString()
}

// ScanTime reads a non-nullable timestamp column into *time.Time. The pgx
// codec delivers a time.Time directly; we assert it.
func ScanTime(src any, dst *time.Time) error {
	if dst == nil {
		return fmt.Errorf("scan time: nil dst")
	}
	if src == nil {
		return fmt.Errorf("scan time: unexpected NULL for non-nullable column")
	}
	t, ok := src.(time.Time)
	if !ok {
		return fmt.Errorf("scan time: unexpected type %T", src)
	}
	*dst = t
	return nil
}

// ScanNullTime reads a nullable timestamp column into **time.Time (pointer
// to pointer). The outer pointer must not be nil; the inner pointer is set
// to nil on SQL NULL and to a parsed value otherwise.
func ScanNullTime(src any, dst **time.Time) error {
	if dst == nil {
		return fmt.Errorf("scan null time: nil dst")
	}
	if src == nil {
		*dst = nil
		return nil
	}
	var t time.Time
	if err := ScanTime(src, &t); err != nil {
		return err
	}
	*dst = &t
	return nil
}

// ScanStringSlice decodes a TEXT[] value retrieved from the database into
// *[]string. Depending on the scan destination the pgx codec hands us either
// a ready-made []string or the raw array literal, so both are handled.
//
// Nil source produces a nil slice, matching Postgres' empty array literal.
func ScanStringSlice(src any, dst *[]string) error {
	if dst == nil {
		return fmt.Errorf("scan string slice: nil dst")
	}
	if src == nil {
		*dst = nil
		return nil
	}
	switch v := src.(type) {
	case []string:
		*dst = append((*dst)[:0], v...)
		return nil
	case string:
		parsed, err := parsePGTextArray(v)
		if err != nil {
			return fmt.Errorf("scan string slice: %w", err)
		}
		*dst = parsed
		return nil
	case []byte:
		parsed, err := parsePGTextArray(string(v))
		if err != nil {
			return fmt.Errorf("scan string slice: %w", err)
		}
		*dst = parsed
		return nil
	default:
		return fmt.Errorf("scan string slice: unexpected type %T", src)
	}
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
