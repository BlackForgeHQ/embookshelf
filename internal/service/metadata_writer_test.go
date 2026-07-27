// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// fakeBookWriter records UpdateMetadata calls for the test.
type fakeBookWriter struct {
	called          []model.Book
	err             error
	folderPathCalls []folderPathCall
	renameTxCalls   []repo.RenameFolderTxArgs
	renameTxErr     error
}

type folderPathCall struct {
	BookID     string
	FolderPath string
	Path       string
}

func (f *fakeBookWriter) UpdateMetadata(ctx context.Context, b model.Book) error {
	f.called = append(f.called, b)
	return f.err
}

func (f *fakeBookWriter) SetFolderPath(ctx context.Context, bookID, folderPath, path string) error {
	f.folderPathCalls = append(f.folderPathCalls, folderPathCall{BookID: bookID, FolderPath: folderPath, Path: path})
	return nil
}

func (f *fakeBookWriter) RenameFolderTx(ctx context.Context, args repo.RenameFolderTxArgs) error {
	f.renameTxCalls = append(f.renameTxCalls, args)
	return f.renameTxErr
}

// fakeOrphans records orphan inserts for the rollback path.
type fakeOrphans struct {
	calls [][]repo.PendingOrphanInsert
	err   error
}

func (f *fakeOrphans) Insert(ctx context.Context, rows []repo.PendingOrphanInsert) error {
	f.calls = append(f.calls, rows)
	return f.err
}

// recordingSidecarWriter captures the last Write call args.
type recordingSidecarWriter struct {
	calls []sidecarCall
	err   error
}

type sidecarCall struct {
	Key    string
	Side   sidecar.Sidecar
	Mode   sidecar.WriteMode
	Format string
}

func (r *recordingSidecarWriter) Write(
	ctx context.Context,
	store storage.Storage,
	key string,
	s sidecar.Sidecar,
	mode sidecar.WriteMode,
	format string,
) error {
	r.calls = append(r.calls, sidecarCall{Key: key, Side: s, Mode: mode, Format: format})
	return r.err
}

// newPipelineWriter builds a MetadataWriter over a local library: the
// shape every edit-side caller gets in production, where Write is the
// whole ADR-0001 sequence (DB → in-file embed → sidecar → folder rename)
// rather than a bare UpdateMetadata. A nil dispatch stands for "this
// format has no in-file target", which collapses the sidecar to a full
// mirror.
//
// The returned Storage is the library root — seed a book file into it
// when the test needs the embed step to have something to open. This is
// the one writer harness: LibraryService and EnrichmentService tests
// build their services on top of it rather than faking the pipeline a
// second time.
func newPipelineWriter(
	t *testing.T,
	books BookMetadataWriter,
	side SidecarWriterFor,
	dispatch EmbedderDispatcher,
) (*MetadataWriter, storage.Storage) {
	t.Helper()
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	if dispatch == nil {
		dispatch = func(string) (fileproc.Embedder, error) { return nil, fileproc.ErrUnsupportedEmbed }
	}
	return NewMetadataWriter(MetadataWriterDeps{
		Books: books,
		LibStore: &fakeLibStore{handle: &LibraryHandle{
			Library: model.Library{ID: "lib1"},
			Storage: fs,
		}},
		Sidecar:  side,
		Dispatch: dispatch,
	}), fs
}

func TestMetadataWriter_Write_AutoEnrichment_DBOnly(t *testing.T) {
	books := &fakeBookWriter{}
	mw := NewMetadataWriter(MetadataWriterDeps{Books: books})
	book := model.Book{ID: "b1", Title: "Auto-applied"}
	if _, err := mw.Write(context.Background(), book, TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(books.called) != 1 {
		t.Fatalf("UpdateMetadata called %d times; want 1", len(books.called))
	}
	if books.called[0].Title != "Auto-applied" {
		t.Errorf("Title=%q", books.called[0].Title)
	}
}

func TestMetadataWriter_Write_DBFails_ErrorReturned(t *testing.T) {
	books := &fakeBookWriter{err: context.Canceled}
	mw := NewMetadataWriter(MetadataWriterDeps{Books: books})
	_, err := mw.Write(context.Background(), model.Book{ID: "b1"}, TriggerManualEdit)
	if err == nil {
		t.Fatal("Write: want error, got nil")
	}
}

func TestMetadataWriter_Write_ManualEdit_FiresSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1"},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
	})
	book := model.Book{
		ID:        "b1",
		LibraryID: "lib1",
		Path:      "books/dune.pdf",
		Title:     "Dune",
		Format:    "PDF",
		Tags:      []string{"sf"},
	}
	if _, err := mw.Write(context.Background(), book, TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("Sidecar.Write called %d times; want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.Key != "books/metadata.embookshelf.json" {
		t.Errorf("Key=%q want books/metadata.embookshelf.json", got.Key)
	}
	if got.Format != "PDF" {
		t.Errorf("Format=%q want PDF", got.Format)
	}
}

// fakeEmbedder stubs fileproc.DispatchEmbedder.
type fakeEmbedder struct {
	embeddedFor []string
	out         []byte
	err         error
}

func (f *fakeEmbedder) Embed(ctx context.Context, src storage.Source, in fileproc.EmbedInput) ([]byte, error) {
	f.embeddedFor = append(f.embeddedFor, in.Title)
	return f.out, f.err
}

func TestMetadataWriter_Write_NoDispatch_FullModeSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		// Dispatch deliberately omitted — embed step skipped, sidecar
		// must fall back to ModeFull per ADR-0001.
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: "books/x.epub", Format: "EPUB", Title: "X",
	}
	if _, err := mw.Write(context.Background(), book, TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("sidecar calls: %d want 1", len(rec.calls))
	}
	if rec.calls[0].Mode != sidecar.ModeFull {
		t.Errorf("mode=%q want full (file step skipped: no Dispatch wired)", rec.calls[0].Mode)
	}
	if len(emb.embeddedFor) != 0 {
		t.Errorf("embedder called %d times; want 0 (Dispatch nil)", len(emb.embeddedFor))
	}
}

func TestMetadataWriter_Write_AutoEnrichment_SkipsSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: &LibraryHandle{}},
		Sidecar:  rec,
	})
	if _, err := mw.Write(context.Background(), model.Book{ID: "b1"}, TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("Sidecar.Write called %d times on auto-enrichment; want 0", len(rec.calls))
	}
}

func TestMetadataWriter_Write_EPUBLocal_SpilloverMode(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder-epub-bytes")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Dispatch: func(format string) (fileproc.Embedder, error) {
			if format == "EPUB" {
				return emb, nil
			}
			return nil, fileproc.ErrUnsupportedEmbed
		},
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "Curated",
	}
	if _, err := mw.Write(context.Background(), book, TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(emb.embeddedFor) != 1 {
		t.Fatalf("Embed called %d times; want 1", len(emb.embeddedFor))
	}
	if emb.embeddedFor[0] != "Curated" {
		t.Errorf("Embed input title=%q want Curated", emb.embeddedFor[0])
	}
	if rec.calls[0].Mode != sidecar.ModeSpillover {
		t.Errorf("sidecar mode=%q want spillover (file write succeeded)", rec.calls[0].Mode)
	}

	rc, err := fs.Get(context.Background(), bookKey)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "rezipped-epub-bytes" {
		t.Errorf("file bytes=%q want rezipped-epub-bytes", got)
	}
}

// fakeFileRepo records SetContentHash calls.
type fakeFileRepo struct {
	files     []model.File
	listErr   error
	stamped   []hashStamp
	stampErr  error
	relocated []relocateCall
}

type hashStamp struct {
	FileID string
	Hash   []byte
	Size   int64
}

type relocateCall struct {
	FileID      string
	NewLocation string
}

func (f *fakeFileRepo) ListByBook(ctx context.Context, bookID string) ([]model.File, error) {
	return f.files, f.listErr
}
func (f *fakeFileRepo) SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error {
	f.stamped = append(f.stamped, hashStamp{FileID: fileID, Hash: hash, Size: size})
	return f.stampErr
}
func (f *fakeFileRepo) UpdateLocation(ctx context.Context, fileID, newLocation string) error {
	f.relocated = append(f.relocated, relocateCall{FileID: fileID, NewLocation: newLocation})
	return nil
}

// TestMetadataWriter_Write_UnsupportedFormat_FullMirror covers the
// "format has no in-file write target" row of ADR-0001 §3: when
// Dispatch returns an error, in-file is skipped and the sidecar
// must hold a full mirror.
func TestMetadataWriter_Write_UnsupportedFormat_FullMirror(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Dispatch: func(string) (fileproc.Embedder, error) { return nil, fileproc.ErrUnsupportedEmbed },
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: "books/x.cbz", Format: "CBZ", Title: "X",
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.InFileWritten {
		t.Errorf("InFileWritten=true; want false (Dispatch refused)")
	}
	if out.SidecarMode != sidecar.ModeFull {
		t.Errorf("SidecarMode=%q; want full (in-file skipped → full mirror)", out.SidecarMode)
	}
}

// TestMetadataWriter_Write_EmbedFailure_FullMirror covers the
// "in-file write attempted and failed" row of ADR-0001 §3: when
// Embed returns an error, sidecar mode falls back to full so the
// edit survives.
func TestMetadataWriter_Write_EmbedFailure_FullMirror(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
		Storage: fs,
	}
	emb := &fakeEmbedder{err: errors.New("rezip exploded")}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Dispatch: func(string) (fileproc.Embedder, error) { return emb, nil },
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "X",
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.InFileWritten {
		t.Errorf("InFileWritten=true; want false (Embed errored)")
	}
	if out.SidecarMode != sidecar.ModeFull {
		t.Errorf("SidecarMode=%q; want full (embed failed → full mirror)", out.SidecarMode)
	}
}

// TestMetadataWriter_HashStamp_MultiRow_NoFormatMatch_Skips pins
// the deterministic rule: when a book has >1 files rows and none
// match the just-written format, the stamp is skipped (rather than
// silently stamping the wrong row). Schema permits N>1; today's
// code never creates it; this test makes the future-safe behavior
// explicit.
func TestMetadataWriter_HashStamp_MultiRow_NoFormatMatch_Skips(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped")}
	files := &fakeFileRepo{files: []model.File{
		{ID: "f1", BookID: "b1", Format: "PDF"},
		{ID: "f2", BookID: "b1", Format: "MOBI"},
	}}

	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Files:    files,
		Dispatch: func(format string) (fileproc.Embedder, error) { return emb, nil },
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "X",
	}
	if _, err := mw.Write(context.Background(), book, TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(files.stamped) != 0 {
		t.Errorf("SetContentHash called %d times; want 0 (multi-row, no format match — must not guess)", len(files.stamped))
	}
}

// TestMetadataWriter_Write_ReturnsOutcome pins the new (Outcome,
// error) signature. Outcome carries the post-execution facts the
// SidecarMode rule depends on (InFileWritten) and the format that
// was actually embedded so the hash-stamp call can use it instead
// of inferring from the book's recorded format.
func TestMetadataWriter_Write_ReturnsOutcome_LocalEPUBSuccess(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped")}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Dispatch: func(format string) (fileproc.Embedder, error) { return emb, nil },
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "X",
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !out.InFileWritten {
		t.Errorf("Outcome.InFileWritten=false; want true")
	}
	if out.SidecarMode != sidecar.ModeSpillover {
		t.Errorf("Outcome.SidecarMode=%q; want spillover", out.SidecarMode)
	}
}

func TestMetadataWriter_Write_ReturnsOutcome_AutoEnrichment(t *testing.T) {
	books := &fakeBookWriter{}
	mw := NewMetadataWriter(MetadataWriterDeps{Books: books})
	out, err := mw.Write(context.Background(), model.Book{ID: "b1"}, TriggerAutoEnrichment)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.InFileWritten {
		t.Errorf("Outcome.InFileWritten=true on auto-enrichment; want false")
	}
	if out.SidecarMode != "" {
		t.Errorf("Outcome.SidecarMode=%q on auto-enrichment; want empty (sidecar skipped)", out.SidecarMode)
	}
}

func TestMetadataWriter_HashStampAfterFileWrite(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}

	root := t.TempDir()
	fs, err := local.New(root)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	bookKey := "books/x.epub"
	if _, err := fs.Put(context.Background(), bookKey, strings.NewReader("placeholder")); err != nil {
		t.Fatalf("seed: %v", err)
	}

	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	handle.Storage = fs

	emb := &fakeEmbedder{out: []byte("rezipped-bytes")}
	files := &fakeFileRepo{files: []model.File{{ID: "f1", BookID: "b1", Format: "EPUB"}}}

	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
		Files:    files,
		Dispatch: func(format string) (fileproc.Embedder, error) { return emb, nil },
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: bookKey, Format: "EPUB", Title: "X",
	}
	if _, err := mw.Write(context.Background(), book, TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(files.stamped) != 1 {
		t.Fatalf("SetContentHash called %d times; want 1", len(files.stamped))
	}
	got := files.stamped[0]
	wantHash := sha256.Sum256([]byte("rezipped-bytes"))
	if !bytes.Equal(got.Hash, wantHash[:]) {
		t.Errorf("hash=%x want %x", got.Hash, wantHash)
	}
	if got.Size != int64(len("rezipped-bytes")) {
		t.Errorf("size=%d want %d", got.Size, len("rezipped-bytes"))
	}
	if got.FileID != "f1" {
		t.Errorf("file_id=%q want f1", got.FileID)
	}
}

// TestMetadataWriter_FolderRename_NewLayout exercises ADR-0003 §6
// rename-on-edit for a Book that already lives in a folder. Asserts:
// the dir actually moves on disk, files.location is rewritten to the
// new prefix, and books.folder_path + path are persisted.
func TestMetadataWriter_FolderRename_NewLayout(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New(libRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	// Pre-populate the old folder with the Book file.
	oldFolder := "Tolkien/Hobbit"
	oldDir := libRoot + "/" + oldFolder
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	bookFile := oldDir + "/hobbit.epub"
	if err := os.WriteFile(bookFile, []byte("epub"), 0o644); err != nil {
		t.Fatalf("write book: %v", err)
	}

	books := &fakeBookWriter{}
	files := &fakeFileRepo{
		files: []model.File{
			{ID: "f1", Location: oldFolder + "/hobbit.epub", Format: "EPUB"},
		},
	}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot},
		Storage: fs,
	}
	libStore := &fakeLibStore{handle: handle}

	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: libStore,
		Files:    files,
	})

	folder := oldFolder
	book := model.Book{
		ID:         "b1",
		LibraryID:  "lib1",
		Author:     "Tolkien",
		Title:      "The Hobbit", // changed; old folder was "Hobbit"
		Format:     "EPUB",
		Path:       oldFolder + "/hobbit.epub",
		FolderPath: &folder,
	}

	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !out.FolderRenamed {
		t.Fatalf("expected FolderRenamed=true; outcome=%+v", out)
	}
	wantFolder := "Tolkien/The Hobbit"
	if out.NewFolderPath != wantFolder {
		t.Errorf("NewFolderPath=%q want %q", out.NewFolderPath, wantFolder)
	}

	// Old dir gone, new dir with file present.
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Errorf("old dir still exists: %v", err)
	}
	newFile := libRoot + "/" + wantFolder + "/hobbit.epub"
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("new file missing: %v", err)
	}

	// files.location updated.
	if len(files.relocated) != 1 {
		t.Fatalf("relocated=%d want 1", len(files.relocated))
	}
	if files.relocated[0].NewLocation != wantFolder+"/hobbit.epub" {
		t.Errorf("new location=%q", files.relocated[0].NewLocation)
	}
	// books.folder_path persisted.
	if len(books.folderPathCalls) != 1 {
		t.Fatalf("SetFolderPath calls=%d want 1", len(books.folderPathCalls))
	}
	if books.folderPathCalls[0].FolderPath != wantFolder {
		t.Errorf("persisted folder=%q", books.folderPathCalls[0].FolderPath)
	}
}

// TestMetadataWriter_FolderRename_NoChange covers the no-op case:
// edit doesn't touch Author or Title, so DecideEffects sees
// folderChanged=false and the rename step is skipped.
func TestMetadataWriter_FolderRename_NoChange(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New(libRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	folder := "Tolkien/The Hobbit"
	if err := os.MkdirAll(libRoot+"/"+folder, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	books := &fakeBookWriter{}
	files := &fakeFileRepo{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot},
		Storage: fs,
	}
	libStore := &fakeLibStore{handle: handle}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: libStore,
		Files:    files,
	})

	book := model.Book{
		ID:         "b1",
		LibraryID:  "lib1",
		Author:     "Tolkien",
		Title:      "The Hobbit", // unchanged
		Format:     "EPUB",
		Path:       folder + "/hobbit.epub",
		FolderPath: &folder,
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.FolderRenamed {
		t.Errorf("FolderRenamed=true on no-op edit")
	}
	if len(books.folderPathCalls) != 0 {
		t.Errorf("SetFolderPath called %d times; want 0", len(books.folderPathCalls))
	}
}

// TestMetadataWriter_FolderRename_S3Renames covers ADR-0005: an
// S3-backed library now performs an edit-time folder rename via
// list-prefix + server-side copy + sweeper-deferred delete. Uses
// local.Storage as a stand-in backend (the rename pipeline is
// agnostic to the concrete Storage impl).
func TestMetadataWriter_FolderRename_S3Renames(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New(libRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	backendID := "backend-s3"
	oldFolder := "Tolkien/Hobbit"
	oldDir := libRoot + "/" + oldFolder
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	if err := os.WriteFile(oldDir+"/hobbit.epub", []byte("epub"), 0o644); err != nil {
		t.Fatalf("write book: %v", err)
	}
	if err := os.WriteFile(oldDir+"/metadata.embookshelf.json", []byte("{}"), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	books := &fakeBookWriter{}
	files := &fakeFileRepo{
		files: []model.File{
			{ID: "f1", Location: oldFolder + "/hobbit.epub", Format: "EPUB"},
		},
	}
	orphans := &fakeOrphans{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot, BackendID: &backendID},
		Storage: fs,
	}
	libStore := &fakeLibStore{handle: handle}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:       books,
		LibStore:    libStore,
		Files:       files,
		Orphans:     orphans,
		RenameGrace: time.Hour,
	})

	folder := oldFolder
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: oldFolder + "/hobbit.epub",
		FolderPath: &folder,
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !out.FolderRenamed {
		t.Fatalf("FolderRenamed=false; outcome=%+v", out)
	}
	wantFolder := "Tolkien/The Hobbit"
	if out.NewFolderPath != wantFolder {
		t.Errorf("NewFolderPath=%q want %q", out.NewFolderPath, wantFolder)
	}

	// New keys exist; old keys still exist (sweeper hasn't run).
	if _, err := os.Stat(libRoot + "/" + wantFolder + "/hobbit.epub"); err != nil {
		t.Errorf("new key missing: %v", err)
	}
	if _, err := os.Stat(libRoot + "/" + wantFolder + "/metadata.embookshelf.json"); err != nil {
		t.Errorf("new sidecar missing: %v", err)
	}
	if _, err := os.Stat(oldDir + "/hobbit.epub"); err != nil {
		t.Errorf("old key deleted prematurely; want sweeper-deferred: %v", err)
	}

	// Single RenameFolderTx call carries the orphan inserts for old keys.
	if len(books.renameTxCalls) != 1 {
		t.Fatalf("RenameFolderTx calls=%d want 1", len(books.renameTxCalls))
	}
	tx := books.renameTxCalls[0]
	if tx.NewFolder != wantFolder {
		t.Errorf("tx.NewFolder=%q want %q", tx.NewFolder, wantFolder)
	}
	if len(tx.Files) != 1 || tx.Files[0].Location != wantFolder+"/hobbit.epub" {
		t.Errorf("tx.Files=%+v", tx.Files)
	}
	if len(tx.Orphans) != 2 {
		t.Errorf("tx.Orphans=%d want 2 (epub + sidecar)", len(tx.Orphans))
	}
	for _, o := range tx.Orphans {
		if !strings.HasPrefix(o.Key, oldFolder+"/") {
			t.Errorf("orphan key=%q not under old prefix", o.Key)
		}
		if o.Reason != repo.ReasonOrphanRename {
			t.Errorf("orphan reason=%q", o.Reason)
		}
	}

	// Rollback path not taken.
	if len(orphans.calls) != 0 {
		t.Errorf("rollback orphans called=%d want 0", len(orphans.calls))
	}
}

// TestMetadataWriter_FolderRename_BackendTxFailRollback covers the
// rollback path: phase-1 copy succeeds but the DB tx fails. The
// half-rename new keys must be enqueued with the short rollback
// grace via the standalone Orphans queue.
func TestMetadataWriter_FolderRename_BackendTxFailRollback(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New(libRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	backendID := "backend-s3"
	oldFolder := "Tolkien/Hobbit"
	oldDir := libRoot + "/" + oldFolder
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(oldDir+"/hobbit.epub", []byte("epub"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	books := &fakeBookWriter{renameTxErr: errors.New("simulated tx failure")}
	files := &fakeFileRepo{
		files: []model.File{
			{ID: "f1", Location: oldFolder + "/hobbit.epub", Format: "EPUB"},
		},
	}
	orphans := &fakeOrphans{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot, BackendID: &backendID},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Files:    files,
		Orphans:  orphans,
	})

	folder := oldFolder
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: oldFolder + "/hobbit.epub",
		FolderPath: &folder,
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.FolderRenamed {
		t.Error("FolderRenamed=true; want false on tx failure")
	}

	// One Orphans.Insert call carrying the new-prefix keys with
	// short-grace eligibility.
	if len(orphans.calls) != 1 {
		t.Fatalf("Orphans.Insert calls=%d want 1", len(orphans.calls))
	}
	rows := orphans.calls[0]
	if len(rows) != 1 {
		t.Fatalf("rollback rows=%d want 1", len(rows))
	}
	wantKey := "Tolkien/The Hobbit/hobbit.epub"
	if rows[0].Key != wantKey {
		t.Errorf("rollback key=%q want %q", rows[0].Key, wantKey)
	}
}

// TestMetadataWriter_FolderRename_BackendNoOrphansRepo verifies
// MetadataWriter fails closed when constructed without an Orphans
// queue: the backend rename arm cannot defer the source delete
// safely, so it must not attempt the copy + tx at all.
func TestMetadataWriter_FolderRename_BackendNoOrphansRepo(t *testing.T) {
	libRoot := t.TempDir()
	fs, err := local.New(libRoot)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	backendID := "backend-s3"
	folder := "Tolkien/Hobbit"
	if err := os.MkdirAll(libRoot+"/"+folder, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	books := &fakeBookWriter{}
	handle := &LibraryHandle{
		Library: model.Library{ID: "lib1", Path: libRoot, BackendID: &backendID},
		Storage: fs,
	}
	mw := NewMetadataWriter(MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
	})

	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Author: "Tolkien", Title: "The Hobbit",
		Format: "EPUB", Path: folder + "/hobbit.epub",
		FolderPath: &folder,
	}
	out, err := mw.Write(context.Background(), book, TriggerManualEdit)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if out.FolderRenamed {
		t.Error("FolderRenamed=true with nil Orphans; want false")
	}
	if len(books.renameTxCalls) != 0 {
		t.Errorf("RenameFolderTx called %d times; want 0", len(books.renameTxCalls))
	}
}
