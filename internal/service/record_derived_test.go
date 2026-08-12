// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// recordingDerivedFiles is a DerivedFiles fake whose lookup answer is
// programmable, recording what the record path did to the files table.
type recordingDerivedFiles struct {
	lookupFile model.File
	lookupErr  error

	setID    string
	setHash  []byte
	setSize  int64
	inserted *model.File
}

func (f *recordingDerivedFiles) GetByLocation(_ context.Context, _, _ string) (model.File, error) {
	return f.lookupFile, f.lookupErr
}

func (f *recordingDerivedFiles) SetContentHash(_ context.Context, fileID string, hash []byte, size int64, _ time.Time) error {
	f.setID, f.setHash, f.setSize = fileID, hash, size
	return nil
}

func (f *recordingDerivedFiles) Insert(_ context.Context, file model.File) (model.File, error) {
	file.ID = "inserted-file"
	f.inserted = &file
	return file, nil
}

// derivedFixture stands up a real local library in a temp dir, a staged
// artifact holding content, and a BookOps over both.
func derivedFixture(t *testing.T, files *recordingDerivedFiles, content string) (*BookOps, model.Book, string) {
	t.Helper()
	root := t.TempDir()
	fs, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "l1", Path: root},
		Storage: fs,
	}
	ops := NewBookOps(&fakeLibStore{handle: handle}, files)

	src := filepath.Join(t.TempDir(), "staged.bin")
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	book := model.Book{ID: "b1", LibraryID: "l1", Title: "Dune", Author: "Frank Herbert"}
	return ops, book, src
}

// TestRecordDerivedTransientLookupErrorIsAnError — the drift #307 exists
// to close: a lookup failure that is not "no row" must surface as an
// error, never fall through to an Insert that violates
// UNIQUE(library_id, location) on regeneration.
func TestRecordDerivedTransientLookupErrorIsAnError(t *testing.T) {
	files := &recordingDerivedFiles{lookupErr: errors.New("connection reset")}
	ops, book, src := derivedFixture(t, files, "epub bytes")

	_, err := ops.RecordDerived(context.Background(), book, src, DerivedEPUB)
	if err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want the transient lookup failure surfaced", err)
	}
	if files.inserted != nil {
		t.Fatalf("Insert ran on a transient lookup error: %+v", *files.inserted)
	}
}

// TestRecordDerivedUpdatesExistingRow — regeneration lands on the same
// key, so the previous rendition's row is updated, not duplicated.
func TestRecordDerivedUpdatesExistingRow(t *testing.T) {
	files := &recordingDerivedFiles{lookupFile: model.File{ID: "f-old"}}
	ops, book, src := derivedFixture(t, files, "epub bytes")

	rec, err := ops.RecordDerived(context.Background(), book, src, DerivedEPUB)
	if err != nil {
		t.Fatalf("RecordDerived: %v", err)
	}
	if rec.FileID != "f-old" {
		t.Errorf("FileID = %q, want the existing row's id", rec.FileID)
	}
	if files.setID != "f-old" || files.setSize != int64(len("epub bytes")) {
		t.Errorf("SetContentHash(%q, size %d), want the existing row refreshed with the new size", files.setID, files.setSize)
	}
	if files.inserted != nil {
		t.Errorf("Insert ran although the location already had a row")
	}
}

// TestRecordDerivedInsertsWhenNotFound — a first generation inserts the
// row, format per kind, hash of the staged bytes computed before
// placement consumed the file.
func TestRecordDerivedInsertsWhenNotFound(t *testing.T) {
	kinds := map[DerivedKind]string{
		DerivedEPUB:      "EPUB",
		DerivedNarration: "MP3",
	}
	for kind, format := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			files := &recordingDerivedFiles{lookupErr: repo.ErrNotFound}
			ops, book, src := derivedFixture(t, files, "artifact bytes")

			rec, err := ops.RecordDerived(context.Background(), book, src, kind)
			if err != nil {
				t.Fatalf("RecordDerived: %v", err)
			}
			if rec.FileID != "inserted-file" {
				t.Errorf("FileID = %q, want the inserted row's id", rec.FileID)
			}
			if files.inserted == nil {
				t.Fatal("no files row inserted")
			}
			if files.inserted.Format != format {
				t.Errorf("Format = %q, want %q", files.inserted.Format, format)
			}
			if files.inserted.BookID != "b1" || files.inserted.LibraryID != "l1" {
				t.Errorf("row book/library = %q/%q, want b1/l1", files.inserted.BookID, files.inserted.LibraryID)
			}
			want := sha256.Sum256([]byte("artifact bytes"))
			if !bytes.Equal(files.inserted.ContentHash, want[:]) {
				t.Errorf("ContentHash = %x, want sha256 of the staged bytes", files.inserted.ContentHash)
			}
			if !bytes.Equal(rec.Hash, want[:]) {
				t.Errorf("rec.Hash = %x, want sha256 of the staged bytes", rec.Hash)
			}
			// Placement consumed the staged file — which is why the hash
			// has to be taken first; a post-place hash has nothing to read.
			if _, err := os.Stat(src); !os.IsNotExist(err) {
				t.Errorf("staged file still present: placement did not consume it")
			}
		})
	}
}

// TestRecordDerivedMarkdownRecordsNoFilesRow — ADR-0033 §4: markdown is
// machine feed, not a library artifact; the catalog never sees it.
func TestRecordDerivedMarkdownRecordsNoFilesRow(t *testing.T) {
	files := &recordingDerivedFiles{lookupErr: errors.New("must not be asked")}
	ops, book, src := derivedFixture(t, files, "# markdown")

	rec, err := ops.RecordDerived(context.Background(), book, src, DerivedMarkdown)
	if err != nil {
		t.Fatalf("RecordDerived: %v", err)
	}
	if files.inserted != nil || files.setID != "" {
		t.Errorf("markdown touched the files table: inserted=%v set=%q", files.inserted, files.setID)
	}
	if rec.FileID != "" {
		t.Errorf("FileID = %q, want empty for markdown", rec.FileID)
	}
	if rec.Location == "" || rec.Size != int64(len("# markdown")) {
		t.Errorf("record = %+v, want the placement's location and size", rec)
	}
}

// failingLibStore refuses every resolve, the in-package twin of
// service_test's unresolvableStore.
type failingLibStore struct{ err error }

func (s failingLibStore) For(context.Context, string) (*LibraryHandle, error) {
	return nil, s.err
}

// TestRecordDerivedResolveLibraryFailure — the resolve arm surfaces
// through the module's interface, like every other BookOps operation.
func TestRecordDerivedResolveLibraryFailure(t *testing.T) {
	ops := NewBookOps(failingLibStore{err: errors.New("backend down")}, &recordingDerivedFiles{})
	book := model.Book{ID: "b1", LibraryID: "l1", Title: "T", Author: "A"}

	src := filepath.Join(t.TempDir(), "staged.bin")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	if _, err := ops.RecordDerived(context.Background(), book, src, DerivedEPUB); err == nil || !strings.Contains(err.Error(), "resolve library") {
		t.Errorf("err = %v, want a resolve-library failure", err)
	}
}
