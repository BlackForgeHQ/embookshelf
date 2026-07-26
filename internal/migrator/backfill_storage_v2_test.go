// SPDX-License-Identifier: AGPL-3.0-or-later

package migrator_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/migrator"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// insertLibrary inserts a minimal library row for testing.
func insertLibrary(t *testing.T, ctx context.Context, d *db.DB, id, path string) {
	t.Helper()
	// Each column gets its own placeholder: reusing $1 across an id
	// (uuid) and a name (text) makes Postgres deduce conflicting types
	// for one parameter (SQLSTATE 42P08).
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO libraries (id, name, slug, path)
		VALUES ($1, $2, $3, $4)
	`, id, id, id, path)
	if err != nil {
		t.Fatalf("insertLibrary(%s): %v", id, err)
	}
}

// insertBook inserts a minimal book row for testing.
func insertBook(t *testing.T, ctx context.Context, d *db.DB, id, libraryID, path, format string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := d.SQL.ExecContext(ctx, `
		INSERT INTO books (id, library_id, title, path, format, updated_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`, id, libraryID, id, path, format, now)
	if err != nil {
		t.Fatalf("insertBook(%s): %v", id, err)
	}
}

// countRows returns the count of rows in the given table.
func countRows(t *testing.T, ctx context.Context, d *db.DB, table string) int {
	t.Helper()
	var n int
	//nolint:gosec // table is a test-internal constant, not user input
	if err := d.SQL.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// getLibraryBackendID returns the backend_id for a library, or empty string if NULL.
func getLibraryBackendID(t *testing.T, ctx context.Context, d *db.DB, libID string) string {
	t.Helper()
	var bid sql.NullString
	if err := d.SQL.QueryRowContext(ctx,
		`SELECT backend_id FROM libraries WHERE id = $1`, libID,
	).Scan(&bid); err != nil {
		t.Fatalf("getLibraryBackendID(%s): %v", libID, err)
	}
	if !bid.Valid {
		return ""
	}
	return bid.String
}

// getBookUUID returns the uuid for a book, or empty string if NULL.
func getBookUUID(t *testing.T, ctx context.Context, d *db.DB, bookID string) string {
	t.Helper()
	var u sql.NullString
	if err := d.SQL.QueryRowContext(ctx,
		`SELECT uuid FROM books WHERE id = $1`, bookID,
	).Scan(&u); err != nil {
		t.Fatalf("getBookUUID(%s): %v", bookID, err)
	}
	if !u.Valid {
		return ""
	}
	return u.String
}

// getFileLocation returns the location for the single file row associated with
// a book. Fails the test if there is not exactly one file row.
func getFileLocation(t *testing.T, ctx context.Context, d *db.DB, bookID string) string {
	t.Helper()
	var loc string
	if err := d.SQL.QueryRowContext(ctx,
		`SELECT location FROM files WHERE book_id = $1`, bookID,
	).Scan(&loc); err != nil {
		t.Fatalf("getFileLocation(%s): %v", bookID, err)
	}
	return loc
}

// getSentinel returns the raw value stored for storage_v2_backfilled, or
// empty string when absent.
func getSentinel(t *testing.T, ctx context.Context, d *db.DB) string {
	t.Helper()
	var v string
	err := d.SQL.QueryRowContext(ctx,
		`SELECT value FROM app_settings WHERE name = 'storage_v2_backfilled'`,
	).Scan(&v)
	if err != nil {
		return ""
	}
	return v
}

// --- Tests ---

// TestBackfillStorageV2_EmptyDB verifies that running the backfill on a
// freshly migrated DB with no libraries or books is a no-op that still writes
// the sentinel.
func TestBackfillStorageV2_EmptyDB(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("BackfillStorageV2: %v", err)
	}

	if countRows(t, ctx, d, "storage_backends") != 0 {
		t.Error("expected 0 storage_backends rows for empty DB")
	}
	if countRows(t, ctx, d, "files") != 0 {
		t.Error("expected 0 files rows for empty DB")
	}
	if s := getSentinel(t, ctx, d); s == "" {
		t.Error("sentinel not written after backfill")
	}
}

// TestBackfillStorageV2_OneLibraryOneBook exercises the happy path:
//   - One library with a non-empty path.
//   - One book in that library with an absolute path under the library root.
//
// After backfill we expect:
//   - 1 storage_backends row.
//   - library.backend_id is set.
//   - book.uuid is non-empty.
//   - 1 files row with a relative location (prefix stripped).
func TestBackfillStorageV2_OneLibraryOneBook(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	const (
		libID  = "11111111-0001-4001-8001-000000000001"
		bookID = "22222222-0001-4001-8001-000000000001"
		root   = "/srv/books"
		bpath  = "/srv/books/Author/Title/Title.epub"
	)
	insertLibrary(t, ctx, d, libID, root)
	insertBook(t, ctx, d, bookID, libID, bpath, "EPUB")

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("BackfillStorageV2: %v", err)
	}

	// Backend created.
	if n := countRows(t, ctx, d, "storage_backends"); n != 1 {
		t.Errorf("storage_backends count = %d, want 1", n)
	}
	// Library wired.
	if bid := getLibraryBackendID(t, ctx, d, libID); bid == "" {
		t.Error("library.backend_id is still NULL after backfill")
	}
	// Book has uuid.
	if u := getBookUUID(t, ctx, d, bookID); u == "" {
		t.Error("book.uuid is still NULL after backfill")
	}
	// Files row with relative location.
	if loc := getFileLocation(t, ctx, d, bookID); loc != "Author/Title/Title.epub" {
		t.Errorf("files.location = %q, want %q", loc, "Author/Title/Title.epub")
	}
	// Sentinel written.
	if s := getSentinel(t, ctx, d); s == "" {
		t.Error("sentinel not written after backfill")
	}
}

// TestBackfillStorageV2_Idempotent verifies that running the backfill twice
// does not duplicate rows or change counts.
func TestBackfillStorageV2_Idempotent(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	const (
		libID  = "11111111-0002-4002-8002-000000000002"
		bookID = "22222222-0002-4002-8002-000000000002"
		root   = "/data/library"
		bpath  = "/data/library/book.epub"
	)
	insertLibrary(t, ctx, d, libID, root)
	insertBook(t, ctx, d, bookID, libID, bpath, "EPUB")

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("first BackfillStorageV2: %v", err)
	}

	beforeBackends := countRows(t, ctx, d, "storage_backends")
	beforeFiles := countRows(t, ctx, d, "files")
	uuid1 := getBookUUID(t, ctx, d, bookID)
	bid1 := getLibraryBackendID(t, ctx, d, libID)

	// Second run — sentinel should cause early exit.
	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("second BackfillStorageV2: %v", err)
	}

	if n := countRows(t, ctx, d, "storage_backends"); n != beforeBackends {
		t.Errorf("storage_backends count changed: before=%d after=%d", beforeBackends, n)
	}
	if n := countRows(t, ctx, d, "files"); n != beforeFiles {
		t.Errorf("files count changed: before=%d after=%d", beforeFiles, n)
	}
	if u := getBookUUID(t, ctx, d, bookID); u != uuid1 {
		t.Errorf("book uuid changed: before=%s after=%s", uuid1, u)
	}
	if bid := getLibraryBackendID(t, ctx, d, libID); bid != bid1 {
		t.Errorf("library backend_id changed: before=%s after=%s", bid1, bid)
	}
}

// TestBackfillStorageV2_EmptyLibraryPath verifies that libraries with an empty
// path are left unwired (no backend created, backend_id stays NULL).
func TestBackfillStorageV2_EmptyLibraryPath(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	const libID = "11111111-000e-400e-800e-00000000000e"
	insertLibrary(t, ctx, d, libID, "")

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("BackfillStorageV2: %v", err)
	}

	// No backends created.
	if n := countRows(t, ctx, d, "storage_backends"); n != 0 {
		t.Errorf("storage_backends count = %d, want 0 for empty-path library", n)
	}
	// Library stays unwired.
	if bid := getLibraryBackendID(t, ctx, d, libID); bid != "" {
		t.Errorf("library.backend_id = %q, want empty for empty-path library", bid)
	}
}

// TestBackfillStorageV2_BookPathOutsideRoot verifies that a book whose path
// does not fall under library.root stores the absolute path verbatim.
func TestBackfillStorageV2_BookPathOutsideRoot(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	const (
		libID  = "11111111-0003-4003-8003-000000000003"
		bookID = "22222222-0003-4003-8003-000000000003"
		root   = "/srv/books"
		// Deliberately outside the root.
		bpath = "/other/place/book.epub"
	)
	insertLibrary(t, ctx, d, libID, root)
	insertBook(t, ctx, d, bookID, libID, bpath, "EPUB")

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("BackfillStorageV2: %v", err)
	}

	// Location must be the absolute path verbatim.
	if loc := getFileLocation(t, ctx, d, bookID); loc != bpath {
		t.Errorf("files.location = %q, want verbatim %q", loc, bpath)
	}
}

// TestBackfillStorageV2_MultipleLibraries verifies that distinct paths produce
// distinct backend rows and each library is correctly wired.
func TestBackfillStorageV2_MultipleLibraries(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()

	const (
		lib1  = "11111111-000a-400a-800a-00000000000a"
		lib2  = "11111111-000b-400b-800b-00000000000b"
		root1 = "/srv/fiction"
		root2 = "/srv/nonfiction"
	)
	insertLibrary(t, ctx, d, lib1, root1)
	insertLibrary(t, ctx, d, lib2, root2)

	insertBook(t, ctx, d, "22222222-000a-400a-800a-00000000000a", lib1, root1+"/Book.epub", "EPUB")
	insertBook(t, ctx, d, "22222222-000b-400b-800b-00000000000b", lib2, root2+"/Doc.pdf", "PDF")

	if err := migrator.BackfillStorageV2(ctx, d); err != nil {
		t.Fatalf("BackfillStorageV2: %v", err)
	}

	if n := countRows(t, ctx, d, "storage_backends"); n != 2 {
		t.Errorf("storage_backends count = %d, want 2", n)
	}
	bid1 := getLibraryBackendID(t, ctx, d, lib1)
	bid2 := getLibraryBackendID(t, ctx, d, lib2)
	if bid1 == "" {
		t.Errorf("library %s has no backend_id", lib1)
	}
	if bid2 == "" {
		t.Errorf("library %s has no backend_id", lib2)
	}
	if bid1 != "" && bid2 != "" && bid1 == bid2 {
		t.Errorf("both libraries share the same backend_id %s; expected distinct IDs", bid1)
	}
	if n := countRows(t, ctx, d, "files"); n != 2 {
		t.Errorf("files count = %d, want 2", n)
	}
}
