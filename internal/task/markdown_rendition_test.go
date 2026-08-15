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
	running    bool
	runningErr error
	ready      bool
	failed     string
	loc        string
	size       int64
	hash       []byte
	version    string
}

func (f *fakeRenditionStore) MarkRunning(context.Context, string) error {
	if f.runningErr != nil {
		return f.runningErr
	}
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
		Record: func(_ context.Context, _ model.Book, src string) (service.DerivedRecord, error) {
			info, err := os.Stat(src)
			if err != nil {
				return service.DerivedRecord{}, err
			}
			return service.DerivedRecord{Location: "A/Sample/Sample.md", Size: info.Size()}, nil
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

// TestMarkdownRenditionFailureArms — one table over (injected failure)
// → (row message, verdict), the choreography renditionRun owns (#302):
// the row is written before the error returns, permanent failures do
// not retry, transient ones do. The stream-open arm is the markdown
// worker's own: the converter POST body comes from the book's bytes.
func TestMarkdownRenditionFailureArms(t *testing.T) {
	cases := map[string]struct {
		mutate    func(*MarkdownRenditionDeps)
		wantRow   string // substring the row must carry
		exactRow  bool
		permanent bool
	}{
		// A disabled extension is still disabled in thirty seconds.
		"not configured is loud and permanent": {
			mutate: func(d *MarkdownRenditionDeps) {
				d.Config = func(context.Context) (repo.ConverterConfig, error) {
					return repo.ConverterConfig{}, nil
				}
			},
			wantRow: "converter extension is not configured", exactRow: true,
			permanent: true,
		},
		// A 422 carries the sidecar's reason onto the row untouched.
		"rejection is verbatim and permanent": {
			mutate: func(d *MarkdownRenditionDeps) {
				d.Convert = func(context.Context, string, io.Reader) (service.ConvertResult, error) {
					return service.ConvertResult{}, &service.ConvertRejectedError{
						Status:  422,
						Message: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
					}
				}
			},
			wantRow: "PDF has no extractable text (Scanned, 1 pages): OCR is required", exactRow: true,
			permanent: true,
		},
		// A sidecar restart must be retried.
		"transient converter error retries": {
			mutate: func(d *MarkdownRenditionDeps) {
				d.Convert = func(context.Context, string, io.Reader) (service.ConvertResult, error) {
					return service.ConvertResult{}, errors.New("converter: dial tcp: connection refused")
				}
			},
			wantRow:   "connection refused",
			permanent: false,
		},
		// EPUB is served natively; routing it through the sidecar is the
		// regression ADR-0033 §2 rejects.
		"non-convertible format refused permanently": {
			mutate: func(d *MarkdownRenditionDeps) {
				epub := pdfBook()
				epub.Format = "EPUB"
				d.Books = fakeBookReader{book: epub}
			},
			wantRow:   "EPUB",
			permanent: true,
		},
		"stream open failure is loud and retryable": {
			mutate: func(d *MarkdownRenditionDeps) {
				d.Open = func(context.Context, model.Book) (io.Reader, int64, io.Closer, error) {
					return nil, 0, nil, errors.New("backend timeout")
				}
			},
			wantRow:   "open book file: backend timeout",
			permanent: false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeRenditionStore{}
			deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})
			tc.mutate(&deps)

			err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps)
			if err == nil {
				t.Fatal("want an error")
			}
			if got := errors.Is(err, jobs.ErrDoNotRetry); got != tc.permanent {
				t.Fatalf("ErrDoNotRetry = %v, want %v (err %v)", got, tc.permanent, err)
			}
			if tc.exactRow && store.failed != tc.wantRow {
				t.Fatalf("row error = %q, want exactly %q", store.failed, tc.wantRow)
			}
			if !strings.Contains(store.failed, tc.wantRow) {
				t.Fatalf("row error = %q, want it to carry %q", store.failed, tc.wantRow)
			}
		})
	}
}

// The finishing tail's ordering invariant is structural now, and this
// pins it: the source hash is read strictly before Record consumes the
// staged file (#341). A Record that removes the staged bytes — which
// the real one does — must not be able to starve the hash read.
func TestMarkdownRenditionReadsTheSourceHashBeforeRecordConsumesTheFile(t *testing.T) {
	store := &fakeRenditionStore{}
	deps := renditionDeps(store, repo.ConverterConfig{Enabled: true, BaseURL: "http://c"})

	var order []string
	deps.SourceHash = func(context.Context, model.Book) []byte {
		order = append(order, "hash")
		return []byte{0xcd}
	}
	record := deps.Record
	deps.Record = func(ctx context.Context, b model.Book, src string) (service.DerivedRecord, error) {
		order = append(order, "record")
		rec, err := record(ctx, b, src)
		// The real Record consumes the staged file (placement moves it).
		_ = os.Remove(src)
		return rec, err
	}

	if err := MarkdownRendition(context.Background(), jobs.MarkdownRenditionArgs{BookID: "b1"}, deps); err != nil {
		t.Fatalf("MarkdownRendition: %v", err)
	}
	if len(order) != 2 || order[0] != "hash" || order[1] != "record" {
		t.Fatalf("order = %v, want the hash read before Record", order)
	}
	if !bytes.Equal(store.hash, []byte{0xcd}) {
		t.Fatalf("hash = %x, want the one read before the file was consumed", store.hash)
	}
}
