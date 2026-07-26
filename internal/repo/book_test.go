// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// These tests guard a coupling nothing else checks: three positionally
// ordered lists that must agree by hand — `bookCols`' SELECT order,
// `scanBook`'s Scan destinations, and `UpdateMetadata`'s SET columns
// against its argument slice.
//
// A count mismatch fails loudly at runtime ("expected N destination
// arguments"). Swapping two *same-type adjacent* columns in one list does
// not: it type-checks, it runs, and every book silently gets those two
// fields crossed. ADR-0023 names this file as the sharpest surviving
// instance of that bug class, and commit 909f6bf fixed exactly this shape
// in user.go once already.
//
// The defence is that every field below carries a value distinct from
// every other field of its type, so any crossing surfaces as a mismatch.
// Booleans can't all be distinct, so the 15 lock flags alternate — which
// catches any adjacent swap, the realistic editing mistake. A swap of two
// same-valued non-adjacent locks would still slip through.

func ptrBool(b bool) *bool { return &b }

// distinctBook returns a book whose every field differs from every other
// field of the same type. tag varies the values so the same helper can
// produce a second, wholly different generation for the update path.
func distinctBook(libraryID, tag string) model.Book {
	pub := time.Date(1991, 3, 17, 0, 0, 0, 0, time.UTC)
	return model.Book{
		LibraryID:     libraryID,
		Title:         tag + "-title",
		Subtitle:      tag + "-subtitle",
		Author:        tag + "-author",
		Format:        "EPUB",
		Year:          1991,
		PublishDate:   &pub,
		Language:      tag + "-lang",
		Rating:        4,
		CoverPalette:  tag + "-palette",
		Description:   tag + "-description",
		ISBN:          "9781111111111",
		ISBN10:        "2222222222",
		Publisher:     tag + "-publisher",
		Series:        tag + "-series",
		SeriesIndex:   7,
		SeriesTotal:   9,
		Genres:        []string{tag + "-genre"},
		Moods:         []string{tag + "-mood"},
		Tags:          []string{tag + "-tag"},
		AgeRating:     tag + "-age",
		ContentRating: tag + "-content",
		Pages:         321,
		PublicReviews: ptrBool(true),
		Locks: model.BookLocks{
			// Alternating so any adjacent pair differs.
			Title: true, Subtitle: false, Author: true,
			Description: false, Publisher: true, Series: false,
			ISBN: true, ISBN10: false, Language: true,
			PublishDate: false, Genres: true, Moods: false,
			Tags: true, Pages: false, Cover: true,
		},
	}
}

// assertDistinctFields compares field by field rather than with a single
// struct equality, so a failure names the columns that crossed.
func assertDistinctFields(t *testing.T, got model.Book, want model.Book) {
	t.Helper()

	for _, c := range []struct {
		field     string
		got, want any
	}{
		{"Title", got.Title, want.Title},
		{"Subtitle", got.Subtitle, want.Subtitle},
		{"Author", got.Author, want.Author},
		{"Language", got.Language, want.Language},
		{"CoverPalette", got.CoverPalette, want.CoverPalette},
		{"Description", got.Description, want.Description},
		{"ISBN", got.ISBN, want.ISBN},
		{"ISBN10", got.ISBN10, want.ISBN10},
		{"Publisher", got.Publisher, want.Publisher},
		{"Series", got.Series, want.Series},
		{"AgeRating", got.AgeRating, want.AgeRating},
		{"ContentRating", got.ContentRating, want.ContentRating},
		{"Year", got.Year, want.Year},
		{"Rating", got.Rating, want.Rating},
		{"SeriesIndex", got.SeriesIndex, want.SeriesIndex},
		{"SeriesTotal", got.SeriesTotal, want.SeriesTotal},
		{"Pages", got.Pages, want.Pages},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v — a column/scan-order crossing looks exactly like this",
				c.field, c.got, c.want)
		}
	}

	for _, c := range []struct {
		field     string
		got, want []string
	}{
		{"Genres", got.Genres, want.Genres},
		{"Moods", got.Moods, want.Moods},
		{"Tags", got.Tags, want.Tags},
	} {
		if len(c.got) != len(c.want) || (len(c.want) > 0 && c.got[0] != c.want[0]) {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}

	if want.PublicReviews != nil {
		if got.PublicReviews == nil || *got.PublicReviews != *want.PublicReviews {
			t.Errorf("PublicReviews = %v, want %v", got.PublicReviews, *want.PublicReviews)
		}
	}
	if want.PublishDate != nil {
		if got.PublishDate == nil || !got.PublishDate.Equal(*want.PublishDate) {
			t.Errorf("PublishDate = %v, want %v", got.PublishDate, *want.PublishDate)
		}
	}
}

// assertLocks checks the 15 lock flags, which form their own positional
// block in both bookCols and scanBook. Split from the field comparison
// because Create deliberately does not persist locks — a freshly imported
// book starts unlocked, and the flags are only ever set by a later
// UpdateMetadata.
func assertLocks(t *testing.T, got, want model.BookLocks) {
	t.Helper()
	for _, c := range []struct {
		field     string
		got, want bool
	}{
		{"Locks.Title", got.Title, want.Title},
		{"Locks.Subtitle", got.Subtitle, want.Subtitle},
		{"Locks.Author", got.Author, want.Author},
		{"Locks.Description", got.Description, want.Description},
		{"Locks.Publisher", got.Publisher, want.Publisher},
		{"Locks.Series", got.Series, want.Series},
		{"Locks.ISBN", got.ISBN, want.ISBN},
		{"Locks.ISBN10", got.ISBN10, want.ISBN10},
		{"Locks.Language", got.Language, want.Language},
		{"Locks.PublishDate", got.PublishDate, want.PublishDate},
		{"Locks.Genres", got.Genres, want.Genres},
		{"Locks.Moods", got.Moods, want.Moods},
		{"Locks.Tags", got.Tags, want.Tags},
		{"Locks.Pages", got.Pages, want.Pages},
		{"Locks.Cover", got.Cover, want.Cover},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v — adjacent lock columns crossed", c.field, c.got, c.want)
		}
	}
}

// Create → GetByID exercises the INSERT's column/arg order and the whole
// bookCols ↔ scanBook read path.
func TestBookRepo_CreateReadRoundTripPreservesEveryField(t *testing.T) {
	d := repotest.New(t)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Round Trip", "round-trip", "/tmp/rt", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	want := distinctBook(lib.ID, "created")
	created, err := br.Create(ctx, want)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertDistinctFields(t, created, want)

	got, err := br.GetByID(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	assertDistinctFields(t, got, want)

	// Create does not carry locks through: the INSERT omits every
	// *_locked column, so a new book starts fully unlocked regardless of
	// what the caller passed. Pinned so the omission stays a choice.
	assertLocks(t, got.Locks, model.BookLocks{})
}

// UpdateMetadata writes 39 columns positionally. A second, wholly
// different generation of values proves the SET list and the argument
// slice still line up — including the 15-flag lock block.
func TestBookRepo_UpdateMetadataWritesEveryFieldToItsOwnColumn(t *testing.T) {
	d := repotest.New(t)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "Update Trip", "update-trip", "/tmp/ut", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	created, err := br.Create(ctx, distinctBook(lib.ID, "first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := distinctBook(lib.ID, "second")
	updated.ID = created.ID
	updated.Year = 2003
	updated.Rating = 2
	updated.SeriesIndex = 3
	updated.SeriesTotal = 4
	updated.Pages = 654
	updated.ISBN = "9783333333333"
	updated.ISBN10 = "4444444444"
	updated.PublicReviews = ptrBool(false)
	// Inverted from distinctBook's pattern, so a stale write shows up.
	updated.Locks = model.BookLocks{
		Title: false, Subtitle: true, Author: false,
		Description: true, Publisher: false, Series: true,
		ISBN: false, ISBN10: true, Language: false,
		PublishDate: true, Genres: false, Moods: true,
		Tags: false, Pages: true, Cover: false,
	}

	if err := br.UpdateMetadata(ctx, updated); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	got, err := br.GetByID(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	assertDistinctFields(t, got, updated)
	assertLocks(t, got.Locks, updated.Locks)
}

// ISBN and ISBN10 are adjacent, same-typed and semantically confusable —
// the pair most likely to be crossed by a careless edit, and the pair a
// crossing would corrupt most invisibly.
func TestBookRepo_ISBNAndISBN10DoNotCross(t *testing.T) {
	d := repotest.New(t)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "ISBN", "isbn", "/tmp/isbn", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	created, err := br.Create(ctx, model.Book{
		LibraryID: lib.ID,
		Title:     "ISBN probe",
		ISBN:      "9789999999999",
		ISBN10:    "1010101010",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := br.GetByID(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ISBN != "9789999999999" {
		t.Errorf("ISBN = %q, want the 13-digit value (crossed with ISBN10?)", got.ISBN)
	}
	if got.ISBN10 != "1010101010" {
		t.Errorf("ISBN10 = %q, want the 10-digit value (crossed with ISBN?)", got.ISBN10)
	}
}

// GetByID accepts an empty user id for callers with no user context
// (backfills, admin reads); the NULLIF cast makes that a supported input
// rather than an "invalid uuid" error.
func TestBookRepo_GetByIDAcceptsEmptyUserID(t *testing.T) {
	d := repotest.New(t)
	lr := repo.NewLibraryRepo(d)
	br := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := lr.CreateLibrary(ctx, "No User", "no-user", "/tmp/nu", nil)
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	created, err := br.Create(ctx, model.Book{LibraryID: lib.ID, Title: "anon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := br.GetByID(ctx, "", created.ID)
	if err != nil {
		t.Fatalf("GetByID with empty user id: %v", err)
	}
	if got.Progress != 0 || got.ResumeCFI != "" {
		t.Errorf("no-user read should yield zero progress, got %d / %q", got.Progress, got.ResumeCFI)
	}
}
