package service_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// fakeBookWriter records UpdateMetadata calls for the test.
type fakeBookWriter struct {
	called []model.Book
	err    error
}

func (f *fakeBookWriter) UpdateMetadata(ctx context.Context, b model.Book) error {
	f.called = append(f.called, b)
	return f.err
}

// fakeLibStore returns a fixed handle for any libraryID.
type fakeLibStore struct {
	handle *service.LibraryHandle
	err    error
}

func (f *fakeLibStore) For(ctx context.Context, libraryID string) (*service.LibraryHandle, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.handle, nil
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

func TestMetadataWriter_Write_AutoEnrichment_DBOnly(t *testing.T) {
	books := &fakeBookWriter{}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	book := model.Book{ID: "b1", Title: "Auto-applied"}
	if err := mw.Write(context.Background(), book, service.TriggerAutoEnrichment); err != nil {
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
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{Books: books})
	err := mw.Write(context.Background(), model.Book{ID: "b1"}, service.TriggerManualEdit)
	if err == nil {
		t.Fatal("Write: want error, got nil")
	}
}

func TestMetadataWriter_Write_ManualEdit_FiresSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1"},
	}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
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
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("Sidecar.Write called %d times; want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.Key != "books/dune.embookshelf.json" {
		t.Errorf("Key=%q want books/dune.embookshelf.json", got.Key)
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

func TestMetadataWriter_Write_EPUBLocal_FullPipeline(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	emb := &fakeEmbedder{out: []byte("rezipped-epub-bytes")}
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", BackendID: nil},
	}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: handle},
		Sidecar:  rec,
	})
	book := model.Book{
		ID: "b1", LibraryID: "lib1",
		Path: "books/x.epub", Format: "EPUB", Title: "X",
	}
	if err := mw.Write(context.Background(), book, service.TriggerManualEdit); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("sidecar calls: %d want 1", len(rec.calls))
	}
	if rec.calls[0].Mode != sidecar.ModeFull {
		t.Errorf("mode=%q want full (file step skipped: no Storage)", rec.calls[0].Mode)
	}
	if len(emb.embeddedFor) != 0 {
		t.Errorf("embedder called %d times; want 0 (no Storage on handle)", len(emb.embeddedFor))
	}
}

func TestMetadataWriter_Write_AutoEnrichment_SkipsSidecar(t *testing.T) {
	books := &fakeBookWriter{}
	rec := &recordingSidecarWriter{}
	mw := service.NewMetadataWriter(service.MetadataWriterDeps{
		Books:    books,
		LibStore: &fakeLibStore{handle: &service.LibraryHandle{}},
		Sidecar:  rec,
	})
	if err := mw.Write(context.Background(), model.Book{ID: "b1"}, service.TriggerAutoEnrichment); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("Sidecar.Write called %d times on auto-enrichment; want 0", len(rec.calls))
	}
}
