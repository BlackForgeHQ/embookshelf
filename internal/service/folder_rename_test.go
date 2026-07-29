// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// Both arms and the migration, reached directly.
//
// Every rename assertion used to go through a full Write — sanitizing a
// folder name, planning effects, embedding, stamping a hash, writing a
// sidecar — to find out what the rename did. That is why the seam
// discarding the difference between "declined" and "broke" went
// unnoticed for as long as it did (#212).

// localRenameFixture wires a local library rooted at a temp dir with one
// book already in a folder.
func localRenameFixture(t *testing.T) (*MetadataWriter, *LibraryHandle, model.Book, string) {
	t.Helper()
	libRoot := t.TempDir()

	// Rooted at "/", which is what boot builds for a local install
	// (ADR-0030 §1) and what makes the absolute paths this arm hands to
	// MovePrefix the keys the adapter answers to.
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	const oldFolder = "Tolkien/Hobbit"
	oldDir := filepath.Join(libRoot, oldFolder)
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "hobbit.epub"), []byte("epub"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    &fakeBookWriter{},
		LibStore: &fakeLibStore{handle: handle},
		Files: &fakeFileRepo{files: []model.File{
			{ID: "f1", Location: oldFolder + "/hobbit.epub", Format: "EPUB"},
		}},
	})
	folder := oldFolder
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: oldFolder + "/hobbit.epub", FolderPath: &folder,
	}
	return mw, handle, book, libRoot
}

func TestRenameFolderLocalMovesTheFolderAndReportsWhereItLanded(t *testing.T) {
	mw, handle, book, libRoot := localRenameFixture(t)

	res := mw.renameFolder(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if !res.Done {
		t.Fatalf("outcome = %+v, want done", res)
	}
	if res.Folder != "Tolkien/The Hobbit" {
		t.Errorf("folder = %q, want the new one", res.Folder)
	}
	if _, err := os.Stat(filepath.Join(libRoot, res.Folder, "hobbit.epub")); err != nil {
		t.Errorf("the file is not at the reported folder: %v", err)
	}
}

// A rename that has nothing to move is not a failure, and saying so is
// the point of the outcome type: this case used to be indistinguishable
// from a broken rename, so it warned on every edit of such a book.
func TestRenameFolderDeclinesABookNotInTheLayout(t *testing.T) {
	mw, handle, book, _ := localRenameFixture(t)

	res := mw.renameFolder(context.Background(), book, handle, "", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a decline", res)
	}
	if res.Declined == "" {
		t.Error("declined with no reason — the caller has nothing to log")
	}
}

func TestRenameFolderLocalDeclinesALibraryWithNoRoot(t *testing.T) {
	mw, handle, book, _ := localRenameFixture(t)
	handle.Library.Path = ""

	res := mw.renameFolder(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a decline", res)
	}
}

// The other half of the type: a rename that started and broke reports an
// error, so the caller can log it as one.
func TestRenameFolderLocalReportsABrokenMove(t *testing.T) {
	mw, handle, book, _ := localRenameFixture(t)
	boom := errors.New("disk went away")
	handle.Storage = brokenMover{Storage: handle.Storage, err: boom}

	res := mw.renameFolder(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if res.Done {
		t.Fatal("a broken move reported done")
	}
	if !errors.Is(res.Err, boom) {
		t.Errorf("err = %v, want the mover's own error", res.Err)
	}
	if res.Declined != "" {
		t.Errorf("a break was reported as a decline (%q) — the distinction this type exists for", res.Declined)
	}
}

// ADR-0005 is fail-closed: without an orphan queue the source delete
// cannot be deferred safely, so the rename does not start. A decline,
// not a break — nothing was attempted.
func TestRenameFolderBackendDeclinesWithoutAnOrphanQueue(t *testing.T) {
	mw, handle, book, _ := localRenameFixture(t)
	handle.Storage = objectStore{Storage: handle.Storage}

	res := mw.renameFolder(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a fail-closed decline", res)
	}
	if res.Declined == "" {
		t.Error("declined with no reason")
	}
}

// The migration is not a rename and answers separately. An object-store
// book has never had a flat layout (ADR-0003 §7), so there is nothing to
// migrate — a decline rather than an attempted listing.
func TestMigrateToFolderLayoutDeclinesAnObjectStoreBook(t *testing.T) {
	mw, handle, book, _ := localRenameFixture(t)
	handle.Storage = objectStore{Storage: handle.Storage}

	res := mw.migrateToFolderLayout(context.Background(), book, handle, "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a decline", res)
	}
}

// The migration proper: files sitting loose at the library root get the
// Book its first folder, and only this Book's files move.
func TestMigrateToFolderLayoutMovesOnlyThisBooksFiles(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	for _, name := range []string{"hobbit.epub", "someone-elses.epub"} {
		if err := os.WriteFile(filepath.Join(libRoot, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    &fakeBookWriter{},
		LibStore: &fakeLibStore{handle: handle},
		Files:    &fakeFileRepo{files: []model.File{{ID: "f1", Location: "hobbit.epub", Format: "EPUB"}}},
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: "hobbit.epub",
	}

	res := mw.migrateToFolderLayout(context.Background(), book, handle, "Tolkien/The Hobbit")

	if !res.Done {
		t.Fatalf("outcome = %+v, want done", res)
	}
	if _, err := os.Stat(filepath.Join(libRoot, res.Folder, "hobbit.epub")); err != nil {
		t.Errorf("the book's file did not land in its new folder: %v", err)
	}
	// The whole reason this cannot be a prefix move.
	if _, err := os.Stat(filepath.Join(libRoot, "someone-elses.epub")); err != nil {
		t.Errorf("a sibling flat-layout file belonging to another book was swept along: %v", err)
	}
}

// brokenMover fails the move and nothing else.
type brokenMover struct {
	storage.Storage
	err error
}

func (m brokenMover) MovePrefix(context.Context, string, string) (storage.MoveResult, error) {
	return storage.MoveResult{}, m.err
}

// objectStore advertises the capability the rename branches on, so the
// backend arm is reachable without an S3 endpoint.
type objectStore struct{ storage.Storage }

func (objectStore) Capabilities() storage.Capability { return storage.CapObjectStore }
