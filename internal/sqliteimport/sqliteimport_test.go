// SPDX-License-Identifier: AGPL-3.0-or-later

package sqliteimport_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/repo/repotest"
	"github.com/blackforge/embookshelf/internal/sqliteimport"
)

func errorsIs(err, target error) bool { return errors.Is(err, target) }

// pair returns a migrated SQLite source and a migrated Postgres target.
// Postgres is the whole point of the importer, so a run without
// TEST_DATABASE_URL skips rather than pretending to pass.
func pair(t *testing.T) (src *sql.DB, dst *sql.DB) {
	t.Helper()
	srcDB := repotest.NewSQLiteSource(t)
	dstDB := repotest.New(t)
	return srcDB.SQL, dstDB.SQL
}

const (
	userID = "11111111-1111-4111-8111-111111111111"
	libID  = "22222222-2222-4222-8222-222222222222"
	bookID = "33333333-3333-4333-8333-333333333333"
)

// seedSQLite writes one row into the tables that exercise every value
// translation the importer has to get right: JSON-text arrays, RFC3339
// text timestamps, and integer booleans.
func seedSQLite(t *testing.T, src *sql.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	if _, err := src.ExecContext(ctx, `
		INSERT INTO users (id, email, name, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, "reader@example.com", "Reader", "hash", "admin", now, now); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := src.ExecContext(ctx, `
		INSERT INTO libraries (id, name, slug, path, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		libID, "Books", "books", "/data/libraries/books", now); err != nil {
		t.Fatalf("seed libraries: %v", err)
	}
	if _, err := src.ExecContext(ctx, `
		INSERT INTO books (id, library_id, title, author, format,
		                   genres, moods, tags, title_locked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bookID, libID, "Deep Work", "Cal Newport", "epub",
		`["productivity","focus"]`, `["calm"]`, `["desk","reread"]`, 1, now, now); err != nil {
		t.Fatalf("seed books: %v", err)
	}
}

func TestImportCopiesRowsAcrossTables(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)

	rep, err := sqliteimport.Run(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for table, want := range map[string]int{"users": 1, "libraries": 1, "books": 1} {
		if got := rep.Rows[table]; got != want {
			t.Errorf("report rows[%s] = %d, want %d", table, got, want)
		}
	}

	var count int
	if err := dst.QueryRow(`SELECT count(*) FROM books WHERE id = $1`, bookID).Scan(&count); err != nil {
		t.Fatalf("count books: %v", err)
	}
	if count != 1 {
		t.Fatalf("book row not present in Postgres")
	}
}

// JSON-text arrays on SQLite are text[] on Postgres. Getting this wrong
// is silent corruption, so assert through Postgres array semantics
// rather than string comparison.
func TestImportTranslatesJSONArraysToPostgresArrays(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)

	if _, err := sqliteimport.Run(context.Background(), src, dst); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var length int
	if err := dst.QueryRow(`SELECT array_length(genres, 1) FROM books WHERE id = $1`, bookID).Scan(&length); err != nil {
		t.Fatalf("array_length(genres): %v", err)
	}
	if length != 2 {
		t.Fatalf("genres has %d elements, want 2", length)
	}

	var hasFocus bool
	if err := dst.QueryRow(
		`SELECT 'focus' = ANY(genres) FROM books WHERE id = $1`, bookID).Scan(&hasFocus); err != nil {
		t.Fatalf("ANY(genres): %v", err)
	}
	if !hasFocus {
		t.Error("genres did not import as a queryable text[]")
	}
}

// SQLite stores timestamps as RFC3339 TEXT and booleans as 0/1.
func TestImportTranslatesTimestampsAndBooleans(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)

	if _, err := sqliteimport.Run(context.Background(), src, dst); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var createdAt time.Time
	var titleLocked bool
	if err := dst.QueryRow(
		`SELECT created_at, title_locked FROM books WHERE id = $1`, bookID,
	).Scan(&createdAt, &titleLocked); err != nil {
		t.Fatalf("scan created_at/title_locked: %v", err)
	}
	if createdAt.IsZero() {
		t.Error("created_at imported as zero — RFC3339 text was not parsed")
	}
	if !titleLocked {
		t.Error("title_locked imported as false — SQLite integer 1 was not converted")
	}
}

// The SQLite `jobs` table is the polling queue; Postgres uses River's
// own tables. Pending jobs cannot transfer, and the report must say so
// rather than silently dropping them.
func TestImportSkipsSQLiteOnlyJobsTable(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)

	if _, err := src.ExecContext(context.Background(),
		`INSERT INTO jobs (kind, args) VALUES (?, ?)`,
		"library.scan", `{"library_id":"x"}`); err != nil {
		t.Fatalf("seed jobs: %v", err)
	}

	rep, err := sqliteimport.Run(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, copied := rep.Rows["jobs"]; copied {
		t.Error("jobs table must not be copied into Postgres")
	}
	if rep.SkippedJobs != 1 {
		t.Errorf("SkippedJobs = %d, want 1 so the operator is told", rep.SkippedJobs)
	}
}

// Importing into a database that already holds data would interleave two
// libraries. Refuse instead.
func TestImportRefusesNonEmptyTarget(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)

	if _, err := dst.ExecContext(context.Background(), `
		INSERT INTO users (id, email, name, password_hash, role)
		VALUES ($1, $2, $3, $4, $5)`,
		"44444444-4444-4444-8444-444444444444", "existing@example.com", "Existing", "h", "user"); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	_, err := sqliteimport.Run(context.Background(), src, dst)
	if err == nil {
		t.Fatal("want a refusal for a non-empty target, got nil")
	}
	if !isNonEmptyErr(err) {
		t.Errorf("err = %v, want ErrTargetNotEmpty", err)
	}
}

func isNonEmptyErr(err error) bool {
	return err != nil && errorsIs(err, sqliteimport.ErrTargetNotEmpty)
}

// SQLite ships with PRAGMA foreign_keys OFF, so a long-lived install can
// hold rows pointing at deleted parents. Postgres enforces the
// constraint. Aborting the whole import over one stale row would strand
// exactly the users this command exists to rescue, so orphans are
// skipped and reported.
func TestImportSkipsOrphanRowsAndReportsThem(t *testing.T) {
	src, dst := pair(t)
	seedSQLite(t, src)
	ctx := context.Background()

	shelfID := "55555555-5555-4555-8555-555555555555"
	if _, err := src.ExecContext(ctx, `
		INSERT INTO shelves (id, user_id, name, slug) VALUES (?, ?, ?, ?)`,
		shelfID, userID, "Favs", "favs"); err != nil {
		t.Fatalf("seed shelf: %v", err)
	}
	// Points at a book that does not exist. The app enables foreign keys
	// on its own connections, so reproducing what an older or externally
	// written file can hold means pinning one connection with the pragma
	// off — PRAGMA foreign_keys is per-connection.
	conn, err := src.Conn(ctx)
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO shelf_books (shelf_id, book_id) VALUES (?, ?)`,
		shelfID, "99999999-9999-4999-8999-999999999999"); err != nil {
		t.Fatalf("seed orphan shelf_books: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close pinned connection: %v", err)
	}

	rep, err := sqliteimport.Run(ctx, src, dst)
	if err != nil {
		t.Fatalf("Run must survive an orphan row, got: %v", err)
	}
	if rep.Orphans["shelf_books"] != 1 {
		t.Errorf("Orphans[shelf_books] = %d, want 1", rep.Orphans["shelf_books"])
	}

	// The valid rows around it must still land.
	var shelves int
	if err := dst.QueryRow(`SELECT count(*) FROM shelves`).Scan(&shelves); err != nil {
		t.Fatalf("count shelves: %v", err)
	}
	if shelves != 1 {
		t.Errorf("shelves = %d, want 1 — a skipped orphan must not lose good rows", shelves)
	}
	var orphaned int
	if err := dst.QueryRow(`SELECT count(*) FROM shelf_books`).Scan(&orphaned); err != nil {
		t.Fatalf("count shelf_books: %v", err)
	}
	if orphaned != 0 {
		t.Errorf("shelf_books = %d, want 0", orphaned)
	}
}

func TestImportEmptySourceIsNotAnError(t *testing.T) {
	src, dst := pair(t)

	rep, err := sqliteimport.Run(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("Run on empty source: %v", err)
	}
	if rep.Total() != 0 {
		t.Errorf("Total = %d, want 0", rep.Total())
	}
}
