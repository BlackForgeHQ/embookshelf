// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// progressFixture creates a user and a book to hang progress off.
func progressFixture(t *testing.T, d *db.DB) (userID, bookID string) {
	t.Helper()
	ctx := context.Background()

	user, err := repo.NewUserRepo(d).Create(ctx, "reader@example.com", "Reader", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "Progress", "progress", "/tmp/progress", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	book, err := repo.NewBookRepo(d).Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB", Path: "dune.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	return user.ID, book.ID
}

// A narrated book is opened by two shells that speak different Locator
// kinds, and each used to overwrite the other's position in the one
// resume column. Read → Listen → Read left the reader at the start of
// the book, and the second toggle destroyed a position that had been
// correct before the first (#200).
func TestSetKeepsTheTextAndAudioPositionsApart(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	userID, bookID := progressFixture(t, d)
	progress := repo.NewProgressRepo(d)

	// Read to 40%.
	if err := progress.Set(ctx, userID, bookID, 40, "epubcfi(/6/14!/4/2)"); err != nil {
		t.Fatalf("Set text: %v", err)
	}
	// Listen for a minute.
	if err := progress.Set(ctx, userID, bookID, 3, "time:60.00"); err != nil {
		t.Fatalf("Set audio: %v", err)
	}

	text, audio, err := progress.Resume(ctx, userID, bookID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if text != "epubcfi(/6/14!/4/2)" {
		t.Errorf("text position = %q, want the CFI — listening overwrote where they were reading", text)
	}
	if audio != "time:60.00" {
		t.Errorf("audio position = %q, want the timestamp", audio)
	}
}

// A page locator is a text position too: PDF and comic shells write it,
// and none of them is the narration.
func TestSetTreatsAPageLocatorAsATextPosition(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	userID, bookID := progressFixture(t, d)
	progress := repo.NewProgressRepo(d)

	if err := progress.Set(ctx, userID, bookID, 10, "page:12"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	text, audio, err := progress.Resume(ctx, userID, bookID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if text != "page:12" {
		t.Errorf("text position = %q, want the page locator", text)
	}
	if audio != "" {
		t.Errorf("audio position = %q, want it untouched", audio)
	}
}

// An empty locator still means "just the percentage changed" and must
// not wipe either position — the behaviour the single-column version
// already had, kept for both.
func TestSetWithNoLocatorLeavesBothPositions(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	userID, bookID := progressFixture(t, d)
	progress := repo.NewProgressRepo(d)

	if err := progress.Set(ctx, userID, bookID, 40, "epubcfi(/6/14!/4/2)"); err != nil {
		t.Fatalf("Set text: %v", err)
	}
	if err := progress.Set(ctx, userID, bookID, 55, ""); err != nil {
		t.Fatalf("Set bare: %v", err)
	}

	text, _, err := progress.Resume(ctx, userID, bookID)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if text != "epubcfi(/6/14!/4/2)" {
		t.Errorf("text position = %q, want it intact after a percent-only update", text)
	}
}
