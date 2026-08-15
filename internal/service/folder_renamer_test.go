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

// The renamer is its own module (#256): one entry point, Relocate, that
// owns the migrate-vs-rename dispatch, and its own dependencies rather
// than the writer's.
//
// Every rename assertion used to go through a full Write — sanitizing a
// folder name, planning effects, embedding, stamping a hash, writing a
// sidecar — to find out what the rename did. That is why the seam
// discarding the difference between "declined" and "broke" went
// unnoticed for as long as it did (#212).

// localRenameFixture wires a renamer over a local library rooted at a
// temp dir with one book already in a folder.
func localRenameFixture(t *testing.T) (*FolderRenamer, *LibraryHandle, model.Book, string) {
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
	r := NewFolderRenamer(FolderRenamerDeps{
		Store: &fakeBookWriter{},
		Files: &fakeFileRepo{files: []model.File{
			{ID: "f1", Location: oldFolder + "/hobbit.epub", Format: "EPUB"},
		}},
	})
	folder := oldFolder
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: oldFolder + "/hobbit.epub", FolderPath: &folder,
	}
	return r, handle, book, libRoot
}

func TestRelocateMovesAFolderedBookAndReportsWhereItLanded(t *testing.T) {
	r, handle, book, libRoot := localRenameFixture(t)

	res := r.Relocate(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

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

// The dispatch is the module's own: an empty oldFolder is a flat-layout
// Book, and Relocate answers it with the ADR-0003 §5 migration rather
// than a rename — the position of that branch is stated here and
// nowhere in the write pipeline.
func TestRelocateDispatchesAFlatLayoutBookToTheMigration(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	if err := os.WriteFile(filepath.Join(libRoot, "hobbit.epub"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot},
		Storage: fs,
	}
	r := NewFolderRenamer(FolderRenamerDeps{
		Store: &fakeBookWriter{},
		Files: &fakeFileRepo{files: []model.File{{ID: "f1", Location: "hobbit.epub", Format: "EPUB"}}},
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: "hobbit.epub",
	}

	res := r.Relocate(context.Background(), book, handle, "", "Tolkien/The Hobbit")

	if !res.Done {
		t.Fatalf("outcome = %+v, want the migration to have run", res)
	}
	if _, err := os.Stat(filepath.Join(libRoot, res.Folder, "hobbit.epub")); err != nil {
		t.Errorf("the book's file did not land in its first folder: %v", err)
	}
}

func TestRelocateDeclinesALibraryWithNoRoot(t *testing.T) {
	r, handle, book, _ := localRenameFixture(t)
	handle.Library.Path = ""

	res := r.Relocate(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a decline", res)
	}
	if res.Declined == "" {
		t.Error("declined with no reason — the caller has nothing to log")
	}
}

// The other half of the type: a rename that started and broke reports an
// error, so the caller can log it as one.
func TestRelocateReportsABrokenMove(t *testing.T) {
	r, handle, book, _ := localRenameFixture(t)
	boom := errors.New("disk went away")
	handle.Storage = brokenMover{Storage: handle.Storage, err: boom}

	res := r.Relocate(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

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
func TestRelocateBackendDeclinesWithoutAnOrphanQueue(t *testing.T) {
	r, handle, book, _ := localRenameFixture(t)
	handle.Storage = objectStore{Storage: handle.Storage}

	res := r.Relocate(context.Background(), book, handle, "Tolkien/Hobbit", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a fail-closed decline", res)
	}
	if res.Declined == "" {
		t.Error("declined with no reason")
	}
}

// An object-store book has never had a flat layout (ADR-0003 §7), so
// a flat-layout dispatch on one has nothing to migrate — a decline
// rather than an attempted listing.
func TestRelocateDeclinesAFlatLayoutObjectStoreBook(t *testing.T) {
	r, handle, book, _ := localRenameFixture(t)
	handle.Storage = objectStore{Storage: handle.Storage}

	res := r.Relocate(context.Background(), book, handle, "", "Tolkien/The Hobbit")

	if res.Done || res.Err != nil {
		t.Fatalf("outcome = %+v, want a decline", res)
	}
}

// The migration proper: files sitting loose at the library root get the
// Book its first folder, and only this Book's files move.
func TestRelocateMigrationMovesOnlyThisBooksFiles(t *testing.T) {
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
	r := NewFolderRenamer(FolderRenamerDeps{
		Store: &fakeBookWriter{},
		Files: &fakeFileRepo{files: []model.File{{ID: "f1", Location: "hobbit.epub", Format: "EPUB"}}},
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: "hobbit.epub",
	}

	res := r.Relocate(context.Background(), book, handle, "", "Tolkien/The Hobbit")

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

// A target folder that is not inside the library is refused before
// anything moves (#323). It used to be trimmed by hand: the trim did
// not match, the absolute path came back, MovePrefix moved the library
// root itself, and books.folder_path was written absolute — one of the
// two producers of such rows ADR-0030 names.
func TestRelocateRefusesATargetOutsideTheLibraryInsteadOfWritingItAbsolute(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	oldDir := filepath.Join(dir, "Tolkien", "Hobbit")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: dir},
		Storage: fs,
	}
	store := &fakeBookWriter{}
	r := NewFolderRenamer(FolderRenamerDeps{Store: store, Files: &fakeFileRepo{}})
	folder := "Tolkien/Hobbit"
	book := model.Book{
		ID: "b1", LibraryID: "lib1", Format: "EPUB",
		Path: folder + "/hobbit.epub", FolderPath: &folder,
	}

	// Empty and ".." both resolve to something that is not a folder
	// inside the library; the rename arm and the flat-layout migration
	// arm each get one.
	for _, tc := range []struct{ oldFolder, newFolder string }{
		{"Tolkien/Hobbit", ""},
		{"", "../escaped"},
	} {
		res := r.Relocate(context.Background(), book, handle, tc.oldFolder, tc.newFolder)

		if res.Done {
			t.Errorf("relocate to %q reported done", tc.newFolder)
		}
		if res.Err == nil {
			t.Errorf("relocate to %q reported no error", tc.newFolder)
		}
	}

	if len(store.folderPathCalls) != 0 {
		t.Errorf("folder_path was written anyway: %+v", store.folderPathCalls)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Errorf("the book's folder moved: %v", err)
	}
	if _, err := os.Stat(dir + " (2)"); err == nil {
		t.Error("the library root itself was probed and renamed to a sibling")
	}
}

// brokenMover fails the move and nothing else. It carries the
// PrefixMover extension because the backend arm requires one — the
// compensation the arm exists for is fed by the report (#345).
type brokenMover struct {
	storage.Storage
	err error
}

func (m brokenMover) MovePrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	_, err := m.MovePrefixDetailed(ctx, oldPrefix, newPrefix)
	return err
}

func (m brokenMover) MovePrefixDetailed(context.Context, string, string) (storage.MoveResult, error) {
	return storage.MoveResult{}, m.err
}

// objectStore advertises the capability the rename branches on, so the
// backend arm is reachable without an S3 endpoint.
type objectStore struct{ storage.Storage }

func (objectStore) Capabilities() storage.Capability { return storage.CapObjectStore }
