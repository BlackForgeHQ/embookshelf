package service_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

func TestMergeLocked_UnlockedFieldsOverwritten(t *testing.T) {
	current := model.Book{
		Title:  "DB Title",
		Author: "DB Author",
		Locks:  model.BookLocks{},
	}
	extracted := model.Book{
		Title:  "File Title",
		Author: "File Author",
	}
	got := service.MergeLocked(current, extracted)
	if got.Title != "File Title" {
		t.Errorf("Title=%q want File Title (unlocked: file wins)", got.Title)
	}
	if got.Author != "File Author" {
		t.Errorf("Author=%q want File Author (unlocked: file wins)", got.Author)
	}
}

func TestMergeLocked_LockedFieldsKeepDB(t *testing.T) {
	current := model.Book{
		Title:  "DB Title",
		Author: "DB Author",
		Locks: model.BookLocks{
			Title: true,
		},
	}
	extracted := model.Book{
		Title:  "File Title",
		Author: "File Author",
	}
	got := service.MergeLocked(current, extracted)
	if got.Title != "DB Title" {
		t.Errorf("Title=%q want DB Title (locked: DB wins)", got.Title)
	}
	if got.Author != "File Author" {
		t.Errorf("Author=%q want File Author (unlocked: file wins)", got.Author)
	}
}

func TestMergeLocked_PreservesIDsAndStructural(t *testing.T) {
	current := model.Book{
		ID:        "b1",
		LibraryID: "lib1",
		Path:      "books/x.epub",
		Format:    "EPUB",
		Locks:     model.BookLocks{},
	}
	extracted := model.Book{
		Title: "T",
	}
	got := service.MergeLocked(current, extracted)
	if got.ID != "b1" {
		t.Errorf("ID lost: %q", got.ID)
	}
	if got.LibraryID != "lib1" {
		t.Errorf("LibraryID lost: %q", got.LibraryID)
	}
	if got.Path != "books/x.epub" {
		t.Errorf("Path lost: %q", got.Path)
	}
	if got.Format != "EPUB" {
		t.Errorf("Format lost: %q", got.Format)
	}
	if got.Title != "T" {
		t.Errorf("Title not applied: %q", got.Title)
	}
}
