// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestBookDropRepo_contentHash verifies that SetContentHash persists the
// SHA-256 bytes on a bookdrop row and that they round-trip through the scan.
func TestBookDropRepo_contentHash(t *testing.T) {
	d := repotest.New(t)
	bdr := repo.NewBookDropRepo(d)
	ctx := t.Context()

	// 1. Insert a bookdrop item; content_hash should be nil.
	item, err := bdr.Insert(ctx, "/drop/book.epub", "EPUB", 1024)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if item.ContentHash != nil {
		t.Fatalf("ContentHash should be nil on fresh insert, got %v", item.ContentHash)
	}

	// 2. SetContentHash writes the bytes.
	h := sha256.Sum256([]byte("file content"))
	hashBytes := h[:]
	if err := bdr.SetContentHash(ctx, item.ID, hashBytes); err != nil {
		t.Fatalf("SetContentHash: %v", err)
	}

	// 3. Read back and confirm round-trip.
	got, err := bdr.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after SetContentHash: %v", err)
	}
	if !bytes.Equal(got.ContentHash, hashBytes) {
		t.Fatalf("ContentHash mismatch: got %x, want %x", got.ContentHash, hashBytes)
	}

	// 4. SetContentHash on a missing id → ErrNotFound.
	err = bdr.SetContentHash(ctx, "00000000-0000-0000-0000-000000000000", hashBytes)
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("SetContentHash missing id: got %v, want ErrNotFound", err)
	}
}

// The bookdrop_items table used to state its twenty-one column list and
// its scan destinations separately, and then patch three fields up after
// the Scan. Six adjacent TEXT columns sit in the middle of that list —
// error_msg, title, author, description, language, isbn — so swapping
// any two of them compiled, ran, and crossed those fields on every row.
// discovered_at/updated_at and cover_mime/book_id are two more adjacent
// same-shaped pairs. This is the Column-order coupling hazard from
// CONTEXT.md.
//
// The defence below is that every field carries a value distinct from
// every other field of its type, so a crossing surfaces as a mismatch.
// The two timestamps are equal on a fresh row and can only be told apart
// once an update has moved updated_at forward, which the round-trip does.

// bookDropBookID creates the library + book a MarkImported'd row points
// at, so book_id is a real, non-NULL uuid during the round-trip. A NULL
// there would hide a crossing with the TEXT column beside it.
func bookDropBookID(t *testing.T, d *db.DB) string {
	t.Helper()
	ctx := t.Context()

	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "BookDrop", "bookdrop", "/tmp/bookdrop", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	book, err := repo.NewBookRepo(d).Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Neuromancer", Author: "William Gibson",
		Format: "EPUB", Path: "neuromancer.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	return book.ID
}

// distinctBookDrop is the row every write path below builds up to. No
// two fields of the same Go type share a value, and the three integers
// are deliberately unequal so a crossing of file_size, progress and
// duration_seconds cannot hide behind a coincidence.
func distinctBookDrop(bookID string) model.BookDropItem {
	duration := 1234
	hash := sha256.Sum256([]byte("bookdrop-round-trip"))
	return model.BookDropItem{
		Path:        "/drop/distinct-path.m4b",
		FileSize:    987654321,
		Format:      "distinct-format",
		State:       model.BookDropFailed,
		Progress:    42,
		ErrorMsg:    "distinct-error-msg",
		Title:       "distinct-title",
		Author:      "distinct-author",
		Description: "distinct-description",
		Language:    "distinct-language",
		ISBN:        "distinct-isbn",
		HasCover:    true,
		CoverMime:   "distinct/cover-mime",
		BookID:      &bookID,
		ContentHash: hash[:],

		DurationSeconds: &duration,
		Narrator:        "distinct-narrator",
		Chapters: []model.Chapter{
			{Title: "distinct-chapter-one", StartS: 1.5, EndS: 2.5},
			{Title: "distinct-chapter-two", StartS: 3.5, EndS: 4.5},
		},
	}
}

// assertBookDropFields compares field by field rather than with a single
// struct equality, so a failure names the columns that crossed.
func assertBookDropFields(t *testing.T, where string, got, want model.BookDropItem) {
	t.Helper()

	const crossed = "%s: %s = %v, want %v — a column/scan-order crossing looks exactly like this"
	for _, c := range []struct {
		field     string
		got, want string
	}{
		{"Path", got.Path, want.Path},
		{"Format", got.Format, want.Format},
		{"State", string(got.State), string(want.State)},
		{"ErrorMsg", got.ErrorMsg, want.ErrorMsg},
		{"Title", got.Title, want.Title},
		{"Author", got.Author, want.Author},
		{"Description", got.Description, want.Description},
		{"Language", got.Language, want.Language},
		{"ISBN", got.ISBN, want.ISBN},
		{"CoverMime", got.CoverMime, want.CoverMime},
		{"Narrator", got.Narrator, want.Narrator},
	} {
		if c.got != c.want {
			t.Errorf(crossed, where, c.field, c.got, c.want)
		}
	}
	if got.FileSize != want.FileSize {
		t.Errorf(crossed, where, "FileSize", got.FileSize, want.FileSize)
	}
	if got.Progress != want.Progress {
		t.Errorf(crossed, where, "Progress", got.Progress, want.Progress)
	}
	if got.HasCover != want.HasCover {
		t.Errorf(crossed, where, "HasCover", got.HasCover, want.HasCover)
	}
	if !bytes.Equal(got.ContentHash, want.ContentHash) {
		t.Errorf(crossed, where, "ContentHash", got.ContentHash, want.ContentHash)
	}

	switch {
	case got.BookID == nil && want.BookID != nil:
		t.Errorf("%s: BookID is nil, want %q", where, *want.BookID)
	case got.BookID != nil && want.BookID == nil:
		t.Errorf("%s: BookID = %q, want nil", where, *got.BookID)
	case got.BookID != nil && *got.BookID != *want.BookID:
		t.Errorf(crossed, where, "BookID", *got.BookID, *want.BookID)
	}
	// book_id sits directly beside cover_mime, and id is the column
	// before path. Neither may end up holding the other's value.
	if got.BookID != nil && *got.BookID == got.ID {
		t.Errorf("%s: BookID = ID = %q — the two uuid columns crossed", where, got.ID)
	}
	if got.ID == got.Path {
		t.Errorf("%s: ID = Path = %q — the first two columns crossed", where, got.ID)
	}

	switch {
	case got.DurationSeconds == nil && want.DurationSeconds != nil:
		t.Errorf("%s: DurationSeconds is nil, want %d", where, *want.DurationSeconds)
	case got.DurationSeconds != nil && want.DurationSeconds == nil:
		t.Errorf("%s: DurationSeconds = %d, want nil", where, *got.DurationSeconds)
	case got.DurationSeconds != nil && *got.DurationSeconds != *want.DurationSeconds:
		t.Errorf(crossed, where, "DurationSeconds", *got.DurationSeconds, *want.DurationSeconds)
	}

	if len(got.Chapters) != len(want.Chapters) {
		t.Errorf("%s: %d chapters, want %d", where, len(got.Chapters), len(want.Chapters))
	} else {
		for i := range want.Chapters {
			if got.Chapters[i] != want.Chapters[i] {
				t.Errorf(crossed, where, fmt.Sprintf("Chapters[%d]", i), got.Chapters[i], want.Chapters[i])
			}
		}
	}

	if got.DiscoveredAt.IsZero() {
		t.Errorf("%s: DiscoveredAt is zero — the timestamp columns did not land", where)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("%s: UpdatedAt is zero — the timestamp columns did not land", where)
	}
	// The two TIMESTAMPTZ columns are adjacent and equal on a fresh row.
	// Every write path below has touched updated_at, so this is the one
	// moment a crossing of the pair is visible.
	if !got.UpdatedAt.After(got.DiscoveredAt) {
		t.Errorf("%s: UpdatedAt (%v) is not after DiscoveredAt (%v) — the timestamp columns crossed",
			where, got.UpdatedAt, got.DiscoveredAt)
	}
}

// Insert → SetMetadata → SetAudio → SetContentHash → MarkImported →
// SetState builds one row that has a distinct value in every column,
// then reads it back through all three read paths. Insert's RETURNING
// and the three SELECTs render the same column list, and every read
// lands through the same scan.
func TestBookDropRepo_RoundTripPreservesEveryField(t *testing.T) {
	d := repotest.New(t)
	ctx := t.Context()
	bdr := repo.NewBookDropRepo(d)

	want := distinctBookDrop(bookDropBookID(t, d))

	// Insert exercises the RETURNING form of the column list.
	inserted, err := bdr.Insert(ctx, want.Path, want.Format, want.FileSize)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if inserted.ID == "" {
		t.Fatal("Insert returned an empty id")
	}
	if inserted.Path != want.Path || inserted.Format != want.Format || inserted.FileSize != want.FileSize {
		t.Fatalf("Insert: path/format/size = %q/%q/%d, want %q/%q/%d",
			inserted.Path, inserted.Format, inserted.FileSize, want.Path, want.Format, want.FileSize)
	}
	if inserted.State != model.BookDropDiscovered {
		t.Errorf("Insert: State = %q, want %q", inserted.State, model.BookDropDiscovered)
	}
	if inserted.Chapters != nil {
		t.Errorf("Insert: Chapters = %v, want nil on a fresh row", inserted.Chapters)
	}
	if inserted.DurationSeconds != nil {
		t.Errorf("Insert: DurationSeconds = %v, want nil on a fresh row", *inserted.DurationSeconds)
	}
	want.ID = inserted.ID

	if err := bdr.SetMetadata(ctx, want.ID, want.Title, want.Author, want.Description,
		want.Language, want.ISBN, want.HasCover, want.CoverMime); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if err := bdr.SetAudio(ctx, want.ID, want.DurationSeconds, want.Narrator, want.Chapters); err != nil {
		t.Fatalf("SetAudio: %v", err)
	}
	if err := bdr.SetContentHash(ctx, want.ID, want.ContentHash); err != nil {
		t.Fatalf("SetContentHash: %v", err)
	}
	if err := bdr.MarkImported(ctx, want.ID, *want.BookID); err != nil {
		t.Fatalf("MarkImported: %v", err)
	}
	// Last, so state and progress end on values no other write produces:
	// 'failed' is not what any earlier step set, and 42 is not the 100
	// SetMetadata and MarkImported both write.
	if err := bdr.SetState(ctx, want.ID, want.State, want.Progress, want.ErrorMsg); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	byID, err := bdr.GetByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	assertBookDropFields(t, "GetByID", byID, want)

	byPath, err := bdr.GetByPath(ctx, want.Path)
	if err != nil {
		t.Fatalf("GetByPath: %v", err)
	}
	assertBookDropFields(t, "GetByPath", byPath, want)
	if byPath.ID != want.ID {
		t.Errorf("GetByPath: ID = %q, want %q", byPath.ID, want.ID)
	}

	list, err := bdr.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d rows, want 1", len(list))
	}
	assertBookDropFields(t, "List", list[0], want)

	// Insert on an existing path returns the row as it stands now, so it
	// re-reads the fully populated row through the same scan.
	again, err := bdr.Insert(ctx, want.Path, want.Format, want.FileSize)
	if !errors.Is(err, repo.ErrAlreadyExists) {
		t.Fatalf("Insert on existing path: err = %v, want ErrAlreadyExists", err)
	}
	assertBookDropFields(t, "Insert existing", again, want)
}

// The nullable columns must come back as nil rather than as a zero
// value, and SetAudio(nil chapters) must not be confused with an empty
// list. A scanner adapter that swallowed NULL into a non-nil empty slice
// would pass the round-trip above and fail here.
func TestBookDropRepo_NullableColumnsStayNil(t *testing.T) {
	d := repotest.New(t)
	ctx := t.Context()
	bdr := repo.NewBookDropRepo(d)

	item, err := bdr.Insert(ctx, "/drop/nullable.epub", "EPUB", 1)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// duration NULL, chapters NULL, narrator ''.
	if err := bdr.SetAudio(ctx, item.ID, nil, "", nil); err != nil {
		t.Fatalf("SetAudio: %v", err)
	}
	got, err := bdr.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DurationSeconds != nil {
		t.Errorf("DurationSeconds = %d, want nil", *got.DurationSeconds)
	}
	if got.Chapters != nil {
		t.Errorf("Chapters = %v, want nil", got.Chapters)
	}
	if got.BookID != nil {
		t.Errorf("BookID = %q, want nil", *got.BookID)
	}
	if got.ContentHash != nil {
		t.Errorf("ContentHash = %x, want nil", got.ContentHash)
	}

	// An explicitly empty chapter list is stored as an empty JSON array
	// and read back as "no chapter data" — the reader has nothing
	// downstream that tells the two apart, and books.chapters behaves the
	// same way.
	if err := bdr.SetAudio(ctx, item.ID, nil, "", []model.Chapter{}); err != nil {
		t.Fatalf("SetAudio empty: %v", err)
	}
	got, err = bdr.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after empty chapters: %v", err)
	}
	if got.Chapters != nil {
		t.Errorf("Chapters = %v after an empty list, want nil", got.Chapters)
	}
}
