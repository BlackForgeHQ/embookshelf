// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"bytes"
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

type fakeRenditionStore struct {
	running bool
	ready   bool
	failed  string
	loc     string
	size    int64
	hash    []byte
	version string
}

func (f *fakeRenditionStore) MarkRunning(context.Context, string) error {
	f.running = true
	return nil
}

func (f *fakeRenditionStore) MarkReady(_ context.Context, _, loc string, size int64, hash []byte, version string) error {
	f.ready, f.loc, f.size, f.hash, f.version = true, loc, size, hash, version
	return nil
}

func (f *fakeRenditionStore) MarkFailed(_ context.Context, _, msg string) error {
	f.failed = msg
	return nil
}

type fakeBookReader struct{ book model.Book }

func (f fakeBookReader) GetByID(context.Context, string, string) (model.Book, error) {
	return f.book, nil
}

func pdfBook() model.Book {
	return model.Book{ID: "b1", Title: "Sample", Author: "A", Format: "PDF"}
}

func renditionDeps(store *fakeRenditionStore, cfg repo.ConverterConfig) MarkdownRenditionDeps {
	return MarkdownRenditionDeps{
		Config:     func(context.Context) (repo.ConverterConfig, error) { return cfg, nil },
		Renditions: store,
		Books:      fakeBookReader{book: pdfBook()},
		Open: func(context.Context, model.Book) (io.Reader, int64, io.Closer, error) {
			return strings.NewReader("%PDF-"), 5, io.NopCloser(nil), nil
		},
		SourceHash: func(context.Context, model.Book) []byte { return []byte{0xab} },
		Convert: func(_ context.Context, _ string, _ io.Reader) (service.ConvertResult, error) {
			f, err := os.CreateTemp(os.TempDir(), "md-*.md")
			if err != nil {
				return service.ConvertResult{}, err
			}
			_, _ = f.WriteString("# md\n")
			_ = f.Close()
			return service.ConvertResult{Path: f.Name(), Version: "0.1.0"}, nil
		},
		Place: func(_ context.Context, _ model.Book, src string) (service.PlaceResult, error) {
			info, err := os.Stat(src)
			if err != nil {
				return service.PlaceResult{}, err
			}
			return service.PlaceResult{Location: "A/Sample/Sample.md", Size: info.Size()}, nil
		},
	}
}

func TestMarkdownRenditionHappyPath(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})

	if err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps); err != nil {
		t.Fatalf("MarkdownRendition: %v", err)
	}
	if !store.running || !store.ready {
		t.Fatalf("store = %+v", store)
	}
	if store.loc != "A/Sample/Sample.md" || store.version != "0.1.0" {
		t.Fatalf("provenance = %q / %q", store.loc, store.version)
	}
	if !bytes.Equal(store.hash, []byte{0xab}) {
		t.Fatalf("hash = %x", store.hash)
	}
}

// TestMarkdownRenditionNotConfiguredIsLoudAndPermanent — the row says
// exactly "converter extension is not configured", and the job does not
// retry: a disabled extension is still disabled in thirty seconds.
func TestMarkdownRenditionNotConfiguredIsLoudAndPermanent(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{})

	err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps)
	if !errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want ErrDoNotRetry", err)
	}
	if store.failed != "converter extension is not configured" {
		t.Fatalf("row error = %q", store.failed)
	}
}

// TestMarkdownRenditionRejectionIsVerbatimAndPermanent — a 422 carries
// the sidecar's reason onto the row untouched and never retries.
func TestMarkdownRenditionRejectionIsVerbatimAndPermanent(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})
	deps.Convert = func(context.Context, string, io.Reader) (service.ConvertResult, error) {
		return service.ConvertResult{}, &service.ConvertRejectedError{
			Status:  422,
			Message: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
		}
	}

	err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps)
	if !errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want ErrDoNotRetry", err)
	}
	if store.failed != "PDF has no extractable text (Scanned, 1 pages): OCR is required" {
		t.Fatalf("row error = %q, want the sidecar's message verbatim", store.failed)
	}
}

// TestMarkdownRenditionTransientErrorRetries — a network failure records
// the error loudly but stays retryable.
func TestMarkdownRenditionTransientErrorRetries(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})
	deps.Convert = func(context.Context, string, io.Reader) (service.ConvertResult, error) {
		return service.ConvertResult{}, errors.New("converter: dial tcp: connection refused")
	}

	err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps)
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatal("a sidecar restart would never be retried")
	}
	if !strings.Contains(store.failed, "connection refused") {
		t.Fatalf("row error = %q", store.failed)
	}
}

// TestMarkdownRenditionRefusesNonConvertible — EPUB is served natively;
// routing it through the sidecar is the regression ADR-0033 §2 rejects.
func TestMarkdownRenditionRefusesNonConvertible(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})
	epub := pdfBook()
	epub.Format = "EPUB"
	deps.Books = fakeBookReader{book: epub}

	err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps)
	if !errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want ErrDoNotRetry", err)
	}
	if !strings.Contains(store.failed, "EPUB") {
		t.Fatalf("row error = %q", store.failed)
	}
}
