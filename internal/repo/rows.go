// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"fmt"
)

// execer is what execOne needs from a handle. *sql.DB and *sql.Tx both
// satisfy it, so a statement inside a transaction gets the same
// not-found mapping as one outside.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execOne runs a statement that addresses exactly one row by key, and
// maps "matched none" to ErrNotFound.
//
// The mapping used to be restated at thirty-eight call sites, which is
// thirty-eight chances to return a nil error for an update that
// happened to nobody — the failure a caller then reports as success.
// Stating it once also means the sentinel is tested once
// (rows_test.go) rather than relied on everywhere.
//
// Not for statements whose row count is the answer (PurgeExpired,
// DeleteMissingOlderThan) or where zero rows is a legitimate outcome
// (an idempotent insert that conflicts).
func execOne(ctx context.Context, x execer, query string, args ...any) error {
	res, err := x.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// collect drains rows through scan, closes them, and reports the
// iteration error the loop would otherwise be free to forget.
//
// out seeds the result and decides the empty case: pass nil where the
// caller's contract is "nil when there is nothing", and an empty slice
// literal where it is "empty list, not null" — several handlers
// serialize the result straight to JSON, and the two are different
// wire shapes.
func collect[T any](rows *sql.Rows, out []T, scan func(scanner) (T, error)) ([]T, error) {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
