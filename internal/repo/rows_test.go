// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// The not-found mapping used to be restated at every update and delete
// in the package. It is stated once now, so it is tested once here
// rather than trusted at each site.
func TestExecOne_MapsZeroAffectedRowsToNotFound(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	id := seedLibrary(t, d)

	t.Run("a row that exists", func(t *testing.T) {
		if err := execOne(ctx, d.SQL, `UPDATE libraries SET name = $2 WHERE id = $1`, id, "renamed"); err != nil {
			t.Fatalf("execOne: %v", err)
		}
	})

	t.Run("a row that does not", func(t *testing.T) {
		missing := db.NewID()
		err := execOne(ctx, d.SQL, `UPDATE libraries SET name = $2 WHERE id = $1`, missing, "x")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a statement that fails", func(t *testing.T) {
		err := execOne(ctx, d.SQL, `UPDATE libraries SET nope = 1 WHERE id = $1`, id)
		if err == nil {
			t.Fatal("want an error")
		}
		// A broken statement must not be reported as a missing row —
		// the caller would surface a 404 for a server fault.
		if errors.Is(err, ErrNotFound) {
			t.Fatalf("a SQL failure was mapped to ErrNotFound: %v", err)
		}
	})

	t.Run("inside a transaction", func(t *testing.T) {
		tx, err := d.SQL.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := execOne(ctx, tx, `UPDATE libraries SET name = $2 WHERE id = $1`, id, "in-tx"); err != nil {
			t.Fatalf("execOne(tx): %v", err)
		}
	})
}

// A driver that cannot report the affected count is a fault, not a
// missing row.
func TestExecOne_SurfacesARowsAffectedFailure(t *testing.T) {
	boom := errors.New("driver said no")
	err := execOne(context.Background(), brokenExecer{boom}, `UPDATE t SET a = 1`)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("a RowsAffected failure was mapped to ErrNotFound")
	}
}

func TestCollect(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	scanInt := func(s scanner) (int, error) {
		var n int
		err := s.Scan(&n)
		return n, err
	}
	query := func(t *testing.T, q string) *sql.Rows {
		t.Helper()
		rows, err := d.SQL.QueryContext(ctx, q)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		return rows
	}

	t.Run("drains every row in order", func(t *testing.T) {
		got, err := collect(query(t, `SELECT generate_series(1, 3)`), nil, scanInt)
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if len(got) != 3 || got[0] != 1 || got[2] != 3 {
			t.Fatalf("got %v, want [1 2 3]", got)
		}
	})

	// The seed decides the empty case, because several handlers
	// serialize the result straight to JSON and `null` is not `[]`.
	t.Run("a nil seed yields nil", func(t *testing.T) {
		got, err := collect(query(t, `SELECT 1 WHERE false`), nil, scanInt)
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})

	t.Run("an empty seed stays non-nil", func(t *testing.T) {
		got, err := collect(query(t, `SELECT 1 WHERE false`), []int{}, scanInt)
		if err != nil {
			t.Fatalf("collect: %v", err)
		}
		if got == nil {
			t.Fatal("got nil, want an empty non-nil slice")
		}
	})

	t.Run("a scan failure stops the walk", func(t *testing.T) {
		boom := errors.New("scan said no")
		rows := query(t, `SELECT generate_series(1, 3)`)
		defer func() { _ = rows.Close() }()
		_, err := collect(rows, nil, func(scanner) (int, error) { return 0, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
	})

	// The early-return path is the one worth pinning: a drained *sql.Rows
	// closes itself, but one abandoned mid-walk holds its connection until
	// somebody closes it. That was a defer at every call site.
	t.Run("closes the rows when a scan fails mid-walk", func(t *testing.T) {
		rows := query(t, `SELECT generate_series(1, 1000)`)
		defer func() { _ = rows.Close() }()

		_, err := collect(rows, nil, func(scanner) (int, error) {
			return 0, errors.New("scan said no")
		})
		if err == nil {
			t.Fatal("want the scan error")
		}
		// 999 rows are still queued. If collect had not closed, they
		// would still be readable — and the connection still held.
		if rows.Next() {
			t.Fatal("rows are still open after collect returned early")
		}
	})
}

func seedLibrary(t *testing.T, d *db.DB) string {
	t.Helper()
	id := db.NewID()
	_, err := d.SQL.ExecContext(context.Background(),
		`INSERT INTO libraries (id, name, slug, path) VALUES ($1, $2, $3, $4)`,
		id, "execone", "execone", "/tmp/execone")
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	return id
}

type brokenExecer struct{ rowsAffectedErr error }

func (b brokenExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return b, nil
}

func (brokenExecer) LastInsertId() (int64, error) { return 0, nil }

func (b brokenExecer) RowsAffected() (int64, error) { return 0, b.rowsAffectedErr }
