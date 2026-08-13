// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

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

func readyFeed(text string) *service.MarkdownFeed {
	return &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.RenditionReady, Location: "A/S/S.md",
		}},
		Open: func(context.Context, model.Book, string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(text)), nil
		},
		CurrentHash: func(context.Context, model.Book) []byte { return nil },
	}
}

type renditionRowFake struct{ row model.MarkdownRendition }

func (f renditionRowFake) GetByBookID(context.Context, string) (model.MarkdownRendition, error) {
	return f.row, nil
}

// epubRecorder counts Record calls and mirrors production's consuming of
// the staged file; the files-row shape itself is RecordDerived's,
// pinned by the service tests (#307).
type epubRecorder struct {
	calls int
	err   error
}

func (r *epubRecorder) record(_ context.Context, _ model.Book, src string) (service.DerivedRecord, error) {
	r.calls++
	if r.err != nil {
		return service.DerivedRecord{}, r.err
	}
	info, err := os.Stat(src)
	if err != nil {
		return service.DerivedRecord{}, err
	}
	_ = os.Remove(src)
	return service.DerivedRecord{
		FileID: "file-new", Location: "A/Sample/Sample.epub", Size: info.Size(),
	}, nil
}

func epubDeps(store *fakeEpubStore, rec *epubRecorder) EpubRenderDeps {
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
		Record: rec.record,
	}
}

func TestEpubRenderHappyPathRecordsTheArtifact(t *testing.T) {
	store := &fakeEpubStore{}
	rec := &epubRecorder{}
	deps := epubDeps(store, rec)

	if err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps); err != nil {
		t.Fatalf("EpubRender: %v", err)
	}
	if !store.ready || store.fileID != "file-new" || store.version != "0.2.0" {
		t.Fatalf("store = %+v", store)
	}
	if rec.calls != 1 {
		t.Fatalf("recorded %d times, want 1", rec.calls)
	}
	if len(store.hash) == 0 || store.hash[0] != 0xcd {
		t.Fatalf("source hash = %x, want the PDF's", store.hash)
	}
}

// TestEpubRenderRecordFailureIsLoudAndRetryable — the recording arm
// (hash, place, files row — all inside Record now) lands on the row and
// stays retryable; the update-vs-insert split itself is RecordDerived's,
// pinned by the service tests (#307).
func TestEpubRenderRecordFailureIsLoudAndRetryable(t *testing.T) {
	store := &fakeEpubStore{}
	rec := &epubRecorder{err: errors.New("backend refused the upload")}
	deps := epubDeps(store, rec)

	err := EpubRender(context.Background(), jobs.EpubRenderArgs{BookID: "b1"}, deps)
	if err == nil || errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want a retryable failure", err)
	}
	if !strings.Contains(store.failed, "record epub") {
		t.Fatalf("row error = %q, want it to name the failing step", store.failed)
	}
}

// TestEpubRenderWaitsForTheMarkdownRendition — a pending markdown stage
// is loud on the row but transient for the queue.
func TestEpubRenderWaitsForTheMarkdownRendition(t *testing.T) {
	store := &fakeEpubStore{}
	deps := epubDeps(store, &epubRecorder{})
	requested := 0
	deps.Markdown = &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.RenditionRunning,
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
	deps := epubDeps(store, &epubRecorder{})
	deps.Markdown = &service.MarkdownFeed{
		Renditions: renditionRowFake{row: model.MarkdownRendition{
			State: model.RenditionFailed,
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

// The shared failure arms (not configured, rejection, transient,
// non-convertible) are TestMarkdownRenditionFailureArms' table plus the
// renditionRun choreography test — what stays here is the EPUB
// worker's own: the markdown chain (wait + verbatim propagation above)
// and the record arm. The files-row shape lives with RecordDerived's
// service tests (#307).
