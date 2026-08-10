// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

type fakeEpubStore struct {
	running bool
	ready   bool
	failed  string
	fileID  string
	hash    []byte
	version string
}

func (f *fakeEpubStore) MarkRunning(context.Context, string) error { f.running = true; return nil }

func (f *fakeEpubStore) MarkReady(_ context.Context, _, fileID string, hash []byte, version string) error {
	f.ready, f.fileID, f.hash, f.version = true, fileID, hash, version
	return nil
}

func (f *fakeEpubStore) MarkFailed(_ context.Context, _, msg string) error {
	f.failed = msg
	return nil
}

type fakeEpubFiles struct {
	byLocation map[string]model.File
	inserted   []model.File
	rehashee   string
}

func (f *fakeEpubFiles) GetByLocation(_ context.Context, _, location string) (model.File, error) {
	if row, ok := f.byLocation[location]; ok {
		return row, nil
	}
	return model.File{}, repo.ErrNotFound
}

func (f *fakeEpubFiles) SetContentHash(_ context.Context, id string, _ []byte, _ int64, _ time.Time) error {
	f.rehashee = id
	return nil
}

func (f *fakeEpubFiles) Insert(_ context.Context, row model.File) (model.File, error) {
	row.ID = "file-new"
	f.inserted = append(f.inserted, row)
	return row, nil
}

func readyFeed(text string) *service.MarkdownFeed {
	return &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.MarkdownRenditionReady, Location: "A/S/S.md",
		}},
		Open: func(context.Context, model.Book, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(text)), nil
		},
	}
}

type renditionRowFake struct{ row model.MarkdownRendition }

func (f renditionRowFake) GetByBookID(context.Context, string) (model.MarkdownRendition, error) {
	return f.row, nil
}

func epubDeps(store *fakeEpubStore, files *fakeEpubFiles) EpubRenderDeps {
	return EpubRenderDeps{
		Config: func(context.Context) (repo.ConverterConfig, error) {
			return repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}, nil
		},
		Renditions: store,
		Books:      fakeBookReader{book: pdfBook()},
		Markdown:   readyFeed("# One\n\nbody\n"),
		SourceHash: func(context.Context, model.Book) []byte { return []byte{0xcd} },
		Render: func(_ context.Context, _ string, req service.EpubRenderRequest) (service.ConvertResult, error) {
			f, err := os.CreateTemp(os.TempDir(), "epub-*.epub")
			if err != nil {
				return service.ConvertResult{}, err
			}
			_, _ = f.WriteString("epub-bytes:" + req.Title)
			_ = f.Close()
			return service.ConvertResult{Path: f.Name(), Version: "0.2.0"}, nil
		},
		Place: func(_ context.Context, _ model.Book, src string) (service.PlaceResult, error) {
			info, err := os.Stat(src)
			if err != nil {
				return service.PlaceResult{}, err
			}
			return service.PlaceResult{Location: "A/Sample/Sample.epub", Size: info.Size()}, nil
		},
		Files: files,
	}
}

func TestEpubRenderHappyPathInsertsAFilesRow(t *testing.T) {
	store := &fakeEpubStore{}
	files := &fakeEpubFiles{}
	deps := epubDeps(store, files)

	if err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps); err != nil {
		t.Fatalf("EpubRender: %v", err)
	}
	if !store.ready || store.fileID != "file-new" || store.version != "0.2.0" {
		t.Fatalf("store = %+v", store)
	}
	if len(files.inserted) != 1 || files.inserted[0].Format != "EPUB" {
		t.Fatalf("inserted = %+v", files.inserted)
	}
	if len(files.inserted[0].ContentHash) == 0 {
		t.Fatal("files row has no content hash — scan's rename safety net keys on it")
	}
	if len(store.hash) == 0 || store.hash[0] != 0xcd {
		t.Fatalf("source hash = %x, want the PDF's", store.hash)
	}
}

// TestEpubRenderRegenerationReusesTheFilesRow — the location is stable,
// so a second render updates the existing row instead of violating
// UNIQUE(library_id, location).
func TestEpubRenderRegenerationReusesTheFilesRow(t *testing.T) {
	store := &fakeEpubStore{}
	files := &fakeEpubFiles{byLocation: map[string]model.File{
		"A/Sample/Sample.epub": {ID: "file-old"},
	}}
	deps := epubDeps(store, files)

	if err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps); err != nil {
		t.Fatalf("EpubRender: %v", err)
	}
	if store.fileID != "file-old" || files.rehashee != "file-old" {
		t.Fatalf("store.fileID = %q, rehashee = %q, want the existing row reused", store.fileID, files.rehashee)
	}
	if len(files.inserted) != 0 {
		t.Fatalf("inserted a duplicate row: %+v", files.inserted)
	}
}

// TestEpubRenderWaitsForTheMarkdownRendition — a pending markdown stage
// is loud on the row but transient for the queue.
func TestEpubRenderWaitsForTheMarkdownRendition(t *testing.T) {
	store := &fakeEpubStore{}
	deps := epubDeps(store, &fakeEpubFiles{})
	requested := 0
	deps.Markdown = &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.MarkdownRenditionRunning,
		}},
		Request: func(context.Context, string) error { requested++; return nil },
	}

	err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps)
	if !errors.Is(err, service.ErrRenditionPending) {
		t.Fatalf("err = %v, want ErrRenditionPending", err)
	}
	if errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatal("pending must stay retryable — the retry is the wait")
	}
	if store.failed != "waiting for the markdown rendition" {
		t.Fatalf("row error = %q", store.failed)
	}
}

// TestEpubRenderPropagatesMarkdownFailureVerbatim — the chained stage's
// message is the row's message (ADR-0034 §5).
func TestEpubRenderPropagatesMarkdownFailureVerbatim(t *testing.T) {
	store := &fakeEpubStore{}
	deps := epubDeps(store, &fakeEpubFiles{})
	deps.Markdown = &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.MarkdownRenditionFailed,
			Error: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
		}},
	}

	err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps)
	if !errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want permanent", err)
	}
	if store.failed != "PDF has no extractable text (Scanned, 1 pages): OCR is required" {
		t.Fatalf("row error = %q, want the markdown stage's message verbatim", store.failed)
	}
}

func TestEpubRenderNotConfiguredIsLoudAndPermanent(t *testing.T) {
	store := &fakeEpubStore{}
	deps := epubDeps(store, &fakeEpubFiles{})
	deps.Config = func(context.Context) (repo.ConverterConfig, error) {
		return repo.ConverterConfig{}, nil
	}

	err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps)
	if !errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want ErrDoNotRetry", err)
	}
	if store.failed != "converter extension is not configured" {
		t.Fatalf("row error = %q", store.failed)
	}
}
