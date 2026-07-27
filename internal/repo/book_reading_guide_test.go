// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// guideFixture creates a library + book and returns the book id.
func guideFixture(t *testing.T, d *db.DB) (string, *repo.BookReadingGuideRepo) {
	t.Helper()
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Guides", "guides", "/tmp/guides", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	b, err := books.Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		Path: "dune.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	return b.ID, repo.NewBookReadingGuideRepo(d)
}

func sampleGuide(bookID string) model.ReadingGuide {
	return model.ReadingGuide{
		BookID: bookID,
		ReadingGuideText: model.ReadingGuideText{
			About:    "Political ecology on a desert planet.",
			Audience: "Readers who like dense worldbuilding.",
			NotFor:   "Anyone wanting a brisk plot.",
			Problems: "Explains how ecology and power interlock.",
		},
		SourceKind: model.GuideSourceFullText,
		Model:      "gpt-4o-mini",
		Language:   "en",
	}
}

func TestBookReadingGuideRepo_UpsertAndGet(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	want := sampleGuide(bookID)
	if err := guides.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := guides.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	// One distinct value per field, so a mis-wired column is caught rather
	// than masked by two fields sharing a value.
	for _, c := range []struct{ field, got, want string }{
		{"About", got.About, want.About},
		{"Audience", got.Audience, want.Audience},
		{"NotFor", got.NotFor, want.NotFor},
		{"Problems", got.Problems, want.Problems},
		{"Model", got.Model, want.Model},
		{"Language", got.Language, want.Language},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if got.SourceKind != model.GuideSourceFullText {
		t.Errorf("SourceKind = %q, want full_text", got.SourceKind)
	}
	if got.EditedByUser {
		t.Error("EditedByUser is true on a freshly generated guide")
	}
	if got.GeneratedAt.IsZero() {
		t.Error("GeneratedAt is zero")
	}
}

func TestBookReadingGuideRepo_GetMissing(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)

	if _, err := guides.GetByBookID(context.Background(), bookID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestBookReadingGuideRepo_UpsertReplaces — regeneration writes over the
// previous guide rather than accumulating rows or failing on the PK.
func TestBookReadingGuideRepo_UpsertReplaces(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	first := sampleGuide(bookID)
	if err := guides.Upsert(ctx, first); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	second := sampleGuide(bookID)
	second.About = "Rewritten by a better model."
	second.Model = "gpt-4o"
	second.SourceKind = model.GuideSourceMetadata
	if err := guides.Upsert(ctx, second); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	got, err := guides.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if got.About != second.About || got.Model != "gpt-4o" {
		t.Fatalf("got %+v, want the second write", got)
	}
	if got.SourceKind != model.GuideSourceMetadata {
		t.Errorf("SourceKind = %q, want the second write's", got.SourceKind)
	}
}

// TestBookReadingGuideRepo_UpsertClearsEditedFlag — a regeneration replaces
// the text, so the row is machine-written again. Leaving the flag set would
// permanently exclude the book from every future bulk run.
func TestBookReadingGuideRepo_UpsertClearsEditedFlag(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	if err := guides.Upsert(ctx, sampleGuide(bookID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := guides.SaveEdit(ctx, bookID, model.ReadingGuideText{
		About: "Hand written.", Audience: "Me.", NotFor: "You.", Problems: "None.",
	}); err != nil {
		t.Fatalf("SaveEdit: %v", err)
	}
	if err := guides.Upsert(ctx, sampleGuide(bookID)); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}

	got, _ := guides.GetByBookID(ctx, bookID)
	if got.EditedByUser {
		t.Fatal("EditedByUser survived a regeneration")
	}
}

// TestBookReadingGuideRepo_SaveEditMarksEdited is what protects hand-written
// text from a bulk run (ADR-0024 §5).
func TestBookReadingGuideRepo_SaveEditMarksEdited(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	if err := guides.Upsert(ctx, sampleGuide(bookID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	edit := model.ReadingGuideText{
		About:    "Actually about power.",
		Audience: "Patient readers.",
		NotFor:   "Skimmers.",
		Problems: "Why empires rot.",
	}
	if err := guides.SaveEdit(ctx, bookID, edit); err != nil {
		t.Fatalf("SaveEdit: %v", err)
	}

	got, err := guides.GetByBookID(ctx, bookID)
	if err != nil {
		t.Fatalf("GetByBookID: %v", err)
	}
	if !got.EditedByUser {
		t.Fatal("EditedByUser is false after a hand edit")
	}
	if got.About != edit.About || got.Problems != edit.Problems {
		t.Errorf("edit not persisted: %+v", got)
	}
	// Provenance of the generation that preceded the edit is kept — it is
	// still what the text was derived from.
	if got.Model != "gpt-4o-mini" {
		t.Errorf("Model = %q, want the generating model preserved", got.Model)
	}
}

func TestBookReadingGuideRepo_SaveEditMissingRow(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)

	err := guides.SaveEdit(context.Background(), bookID, model.ReadingGuideText{About: "x"})
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestBookReadingGuideRepo_DeletedWithBook — the FK cascade keeps guides
// from outliving their book.
func TestBookReadingGuideRepo_DeletedWithBook(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	if err := guides.Upsert(ctx, sampleGuide(bookID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.NewBookRepo(d).Delete(ctx, bookID); err != nil {
		t.Fatalf("delete book: %v", err)
	}
	if _, err := guides.GetByBookID(ctx, bookID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after the book was deleted", err)
	}
}

// TestBookReadingGuideRepo_ListGuideCandidates drives the bulk run: it
// returns books with no guide plus books whose guide is machine-written,
// and never a book whose guide a human edited.
func TestBookReadingGuideRepo_ListGuideCandidates(t *testing.T) {
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	guides := repo.NewBookReadingGuideRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Guides", "guides", "/tmp/guides", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	mk := func(title string) string {
		t.Helper()
		b, err := books.Create(ctx, model.Book{
			LibraryID: lib.ID, Title: title, Format: "EPUB", Path: title + ".epub",
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return b.ID
	}
	none := mk("no-guide")
	generated := mk("generated")
	edited := mk("edited")

	if err := guides.Upsert(ctx, sampleGuide(generated)); err != nil {
		t.Fatalf("Upsert generated: %v", err)
	}
	if err := guides.Upsert(ctx, sampleGuide(edited)); err != nil {
		t.Fatalf("Upsert edited: %v", err)
	}
	if err := guides.SaveEdit(ctx, edited, model.ReadingGuideText{About: "mine"}); err != nil {
		t.Fatalf("SaveEdit: %v", err)
	}

	cands, err := guides.ListGuideCandidates(ctx)
	if err != nil {
		t.Fatalf("ListGuideCandidates: %v", err)
	}
	got := map[string]bool{}
	for _, c := range cands {
		got[c.BookID] = true
		if c.Format != "EPUB" {
			t.Errorf("format for %s = %q, want it carried for the estimate", c.BookID, c.Format)
		}
	}
	if !got[none] {
		t.Error("a book with no guide was not listed")
	}
	if !got[generated] {
		t.Error("a machine-written guide was not listed for regeneration")
	}
	if got[edited] {
		t.Error("a hand-edited guide was listed — a bulk run would erase it")
	}
}

// TestBookReadingGuideRepo_CountCoverage drives the progress bar on a bulk
// run: how many books exist, and how many already have a guide. Both come
// from one query so the two numbers cannot be read a second apart and
// disagree while guides are landing.
func TestBookReadingGuideRepo_CountCoverage(t *testing.T) {
	d := repotest.New(t)
	libs := repo.NewLibraryRepo(d)
	books := repo.NewBookRepo(d)
	guides := repo.NewBookReadingGuideRepo(d)
	ctx := context.Background()

	lib, err := libs.CreateLibrary(ctx, "Guides", "guides", "/tmp/guides", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	mk := func(title string) string {
		t.Helper()
		b, err := books.Create(ctx, model.Book{
			LibraryID: lib.ID, Title: title, Format: "EPUB", Path: title + ".epub",
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return b.ID
	}
	withGuide := mk("has-guide")
	mk("no-guide-1")
	mk("no-guide-2")
	if err := guides.Upsert(ctx, sampleGuide(withGuide)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	total, done, err := guides.CountCoverage(ctx)
	if err != nil {
		t.Fatalf("CountCoverage: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if done != 1 {
		t.Errorf("done = %d, want 1", done)
	}
}

// TestBookReadingGuideRepo_CountCoverageCountsHandEdited — a hand-written
// guide is still a guide. Excluding it would leave the bar stuck below
// 100% forever on a library where someone edited one.
func TestBookReadingGuideRepo_CountCoverageCountsHandEdited(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	if err := guides.Upsert(ctx, sampleGuide(bookID)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := guides.SaveEdit(ctx, bookID, model.ReadingGuideText{About: "mine"}); err != nil {
		t.Fatalf("SaveEdit: %v", err)
	}

	total, done, err := guides.CountCoverage(ctx)
	if err != nil {
		t.Fatalf("CountCoverage: %v", err)
	}
	if total != 1 || done != 1 {
		t.Fatalf("total=%d done=%d, want 1/1 — a hand-edited guide still counts", total, done)
	}
}

// TestBookReadingGuideRepo_CountCoverageIgnoresSoftDeleted — books carry a
// deleted_at column that every query filters on, so coverage must too or a
// soft-deleted book would hold the bar below 100% forever.
//
// Set directly with SQL because nothing in the codebase writes deleted_at
// today: BookRepo.Delete is a hard DELETE. The column and its filters are
// defensive, and a test that went through Delete would pass whether or not
// the filter existed.
func TestBookReadingGuideRepo_CountCoverageIgnoresSoftDeleted(t *testing.T) {
	d := repotest.New(t)
	bookID, guides := guideFixture(t, d)
	ctx := context.Background()

	if _, err := d.SQL.ExecContext(ctx,
		`UPDATE books SET deleted_at = now() WHERE id = $1`, bookID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	total, done, err := guides.CountCoverage(ctx)
	if err != nil {
		t.Fatalf("CountCoverage: %v", err)
	}
	if total != 0 || done != 0 {
		t.Fatalf("total=%d done=%d, want 0/0 for a soft-deleted book", total, done)
	}
}
