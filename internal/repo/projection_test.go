// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// These tests need no database. They pin the *shape* of what each
// projection derives — the rendered SQL text, the placeholder numbering,
// and the one-destination-per-column invariant. Whether a given column
// lands in the right field is a question only a real row can answer, and
// the round-trip tests in book_test.go, user_test.go and the sibling
// repo tests answer it.

// The golden strings below deliberately restate the SQL. That is the
// point: a projection is edited in one place now, and a reorder or
// rename surfaces here as a diff a reviewer has to accept on purpose
// rather than as a silently crossed column.

func TestBookProjection_RendersTheJoinedSelect(t *testing.T) {
	const want = `b.id, b.library_id, b.title, b.subtitle, b.author, b.format, b.year, ` +
		`b.publish_date, b.language, COALESCE(ubp.progress, 0) AS progress, b.rating, ` +
		`b.cover_palette, b.description, b.isbn, b.isbn10, b.publisher, b.series, ` +
		`b.series_index, b.series_total, b.genres, b.moods, b.tags, b.age_rating, ` +
		`b.content_rating, b.pages, b.public_reviews, b.created_at, b.path, b.has_cover, ` +
		`b.cover_mime, b.cover_hash, COALESCE(ubp.resume_cfi, '') AS resume_cfi, COALESCE(ubp.resume_audio, '') AS resume_audio, ` +
		`b.title_locked, b.subtitle_locked, b.author_locked, b.description_locked, ` +
		`b.publisher_locked, b.series_locked, b.isbn_locked, b.isbn10_locked, ` +
		`b.language_locked, b.publish_date_locked, b.genres_locked, b.moods_locked, ` +
		`b.tags_locked, b.pages_locked, b.cover_locked, b.duration_seconds, b.narrator, ` +
		`b.chapters, b.uuid, b.folder_path`
	if bookCols != want {
		t.Fatalf("bookCols =\n%s\nwant\n%s", bookCols, want)
	}
}

// Create used to carry a verbatim copy of the book projection inside its
// CTE. It now renders the shared declaration with the two per-user
// entries overridden, because a row that did not exist a moment ago has
// no user_book_progress row for anybody.
func TestBookCreateQuery_RendersTheSharedProjection(t *testing.T) {
	want := strings.NewReplacer(
		`COALESCE(ubp.progress, 0) AS progress`, `0 AS progress`,
		`COALESCE(ubp.resume_cfi, '') AS resume_cfi`, `'' AS resume_cfi`,
		`COALESCE(ubp.resume_audio, '') AS resume_audio`, `'' AS resume_audio`,
	).Replace(bookCols)
	if !strings.Contains(bookCreateQuery, want) {
		t.Fatalf("create query does not select the shared projection:\n%s", bookCreateQuery)
	}
	if strings.Contains(bookCreateQuery, "ubp.") {
		t.Fatal("create query references the progress join it does not make")
	}
}

// The SET list and its argument accessors come from one walk, so the
// placeholders must run 1..N with no gaps and the row id must follow as
// N+1. UserRepo.TouchLastSeen deliberately numbers $2 before $1 so its
// (id, at) argument order works — that is correct and documented in
// CONTEXT.md, and this invariant is not claimed of it.
func TestBookUpdateMetadata_NumbersPlaceholdersInArgumentOrder(t *testing.T) {
	sets, args := bookProjection.updateSet(1)

	nums := regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(sets, -1)
	if len(nums) != len(args) {
		t.Fatalf("%d placeholders for %d arguments", len(nums), len(args))
	}
	for i, m := range nums {
		if got, _ := strconv.Atoi(m[1]); got != i+1 {
			t.Fatalf("placeholder %d is $%s, want $%d", i, m[1], i+1)
		}
	}

	wantID := "$" + strconv.Itoa(len(args)+1)
	if !strings.Contains(bookUpdateMetadataQuery, "WHERE id = "+wantID+" ") {
		t.Fatalf("row id is not bound at %s:\n%s", wantID, bookUpdateMetadataQuery)
	}
	// Every arg-bearing column, and nothing else, appears in the SET.
	for _, c := range bookProjection {
		inSet := strings.Contains(sets, c.name+" = $")
		if (c.arg != nil) != inSet {
			t.Fatalf("column %q: arg=%v but present in SET=%v", c.name, c.arg != nil, inSet)
		}
	}
}

func TestLibProjection_RendersBothAliasedAndBareForms(t *testing.T) {
	const bookCount = `COALESCE((SELECT COUNT(*) FROM books b WHERE b.library_id = %s.id AND b.deleted_at IS NULL), 0) AS book_count`
	wantSelect := `l.id, l.name, l.slug, l.path, l.last_scanned_at, l.file_count, ` +
		`l.discovered_count, l.created_at, ` +
		strings.Replace(bookCount, "%s", "l", 1) + `, l.backend_id, l.root`
	wantReturning := `id, name, slug, path, last_scanned_at, file_count, ` +
		`discovered_count, created_at, ` +
		strings.Replace(bookCount, "%s", "libraries", 1) + `, backend_id, root`

	if libCols != wantSelect {
		t.Fatalf("libCols =\n%s\nwant\n%s", libCols, wantSelect)
	}
	if libColsReturning != wantReturning {
		t.Fatalf("libColsReturning =\n%s\nwant\n%s", libColsReturning, wantReturning)
	}
}

func TestShelfProjection_RendersItsThreeForms(t *testing.T) {
	bookCount := func(alias string) string {
		return `CASE WHEN ` + alias + `.is_smart THEN 0 ELSE ` +
			`(SELECT count(*) FROM shelf_books sb WHERE sb.shelf_id = ` + alias + `.id) END AS book_count`
	}
	wantSelect := `s.id, s.user_id, s.name, s.slug, s.accent, s.icon, s.created_at, ` +
		`s.is_smart, s.rule, s.is_public, ` + bookCount("s") + `, '' AS owner_name`
	wantReturning := `id, user_id, name, slug, accent, icon, created_at, ` +
		`is_smart, rule, is_public, ` + bookCount("shelves") + `, '' AS owner_name`
	wantVisible := strings.Replace(wantSelect, `'' AS owner_name`,
		`CASE WHEN s.user_id = $1 THEN '' ELSE COALESCE(NULLIF(u.name, ''), u.email, '') END AS owner_name`, 1)

	for _, tc := range []struct{ name, got, want string }{
		{"shelfCols", shelfCols, wantSelect},
		{"shelfColsReturning", shelfColsReturning, wantReturning},
		{"shelfColsVisible", shelfColsVisible, wantVisible},
	} {
		if tc.got != tc.want {
			t.Errorf("%s =\n%s\nwant\n%s", tc.name, tc.got, tc.want)
		}
	}
}

func TestFileProjection_RendersOneBareForm(t *testing.T) {
	const want = `id, library_id, book_id, location, size, mtime, etag, content_hash, ` +
		`format, last_scanned, missing_since`
	if fileCols != want {
		t.Fatalf("fileCols =\n%s\nwant\n%s", fileCols, want)
	}
	// files queries never alias the table, so there is nothing to derive
	// a second time.
	if fileProjection.selectList("f") == fileCols {
		t.Fatal("aliased and bare forms are identical — the alias is not being applied")
	}
}

// with() must leave the column in place, or a query using the variant
// would feed the scanner a differently-ordered row.
func TestProjectionWith_ReplacesOneEntryInPlace(t *testing.T) {
	base := shelfProjection
	variant := base.with("owner_name", `'x' AS owner_name`)

	if len(variant) != len(base) {
		t.Fatalf("variant has %d columns, base has %d", len(variant), len(base))
	}
	for i := range base {
		if base[i].name != variant[i].name {
			t.Fatalf("column %d: %q became %q", i, base[i].name, variant[i].name)
		}
	}
	if base[len(base)-1].expr != `'' AS owner_name` {
		t.Fatal("with() mutated the projection it was called on")
	}
}

func TestProjectionWith_PanicsOnUnknownColumn(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("with(\"nope\") did not panic")
		}
	}()
	_ = shelfProjection.with("nope", "1")
}

// One destination per declared column, each aimed at a distinct field.
// Two entries sharing a destination is what a copy-pasted column looks
// like, and it silently drops one of the two values on every row.
func TestProjections_HaveOneDistinctDestinationPerColumn(t *testing.T) {
	checkProjection(t, "books", bookProjection, &model.Book{})
	checkProjection(t, "libraries", libProjection, &model.Library{})
	checkProjection(t, "shelves", shelfProjection, &model.Shelf{})
	checkProjection(t, "files", fileProjection, &model.File{})
	checkProjection(t, "annotations", annotationProjection, &model.Annotation{})
	checkProjection(t, "bookdrop_items", bookDropProjection, &model.BookDropItem{})
	checkProjection(t, "sessions", sessionProjection, &model.Session{})
	checkProjection(t, "user_invites", userInviteProjection, &UserInvite{})
}

func checkProjection[T any](t *testing.T, table string, p projection[T], zero *T) {
	t.Helper()

	seenName := map[string]bool{}
	seenDest := map[uintptr]string{}
	for _, c := range p {
		if c.name == "" {
			t.Errorf("%s: a column has no name", table)
			continue
		}
		if seenName[c.name] {
			t.Errorf("%s: column %q declared twice", table, c.name)
		}
		seenName[c.name] = true

		ptr := destPointer(c.dest(zero))
		if ptr == 0 {
			t.Errorf("%s: column %q has no usable destination", table, c.name)
			continue
		}
		if prev, dup := seenDest[ptr]; dup {
			t.Errorf("%s: columns %q and %q scan into the same field", table, prev, c.name)
		}
		seenDest[ptr] = c.name
	}

	// scan must hand the driver exactly one destination per column.
	var rec countingScanner
	if err := p.scan(&rec, zero); err != nil {
		t.Fatalf("%s: scan: %v", table, err)
	}
	if rec.n != len(p) {
		t.Errorf("%s: scan passed %d destinations for %d columns", table, rec.n, len(p))
	}
}

// destPointer resolves a scan destination to the address it writes.
// Adapters (db.TextArray, nullText, the JSON columns) wrap the real
// destination in a field named Dst.
func destPointer(dest any) uintptr {
	v := reflect.ValueOf(dest)
	if v.Kind() == reflect.Struct {
		v = v.FieldByName("Dst")
		if !v.IsValid() {
			return 0
		}
	}
	if v.Kind() != reflect.Pointer {
		return 0
	}
	return v.Pointer()
}

type countingScanner struct{ n int }

func (c *countingScanner) Scan(dest ...any) error {
	c.n = len(dest)
	return nil
}
