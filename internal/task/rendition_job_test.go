// SPDX-License-Identifier: AGPL-3.0-or-later

package task

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

type erroringBookReader struct{ err error }

func (f erroringBookReader) GetByID(context.Context, string, string) (model.Book, error) {
	return model.Book{}, f.err
}

func prepareJob(store *fakeRenditionStore) renditionJob {
	return renditionJob{
		Rows:  store,
		Books: fakeBookReader{book: pdfBook()},
		Config: func(context.Context) (repo.ConverterConfig, error) {
			return repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}, nil
		},
		Refusal: func(format string) string { return "format " + format + " is refused for this artifact" },
	}
}

// TestRenditionJobPrepareHappyPath — the four gates in order, ending
// with the row marked running and the facts handed back.
func TestRenditionJobPrepareHappyPath(t *testing.T) {
	store := &fakeRenditionStore{}
	book, cfg, err := prepareJob(store).Prepare(context.Background(), "b1")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if book.ID != "b1" || book.Format != "PDF" {
		t.Errorf("book = %+v, want the loaded row", book)
	}
	if cfg.BaseURL != "http://c" {
		t.Errorf("cfg = %+v, want the read config", cfg)
	}
	if !store.running {
		t.Error("the row was never marked running")
	}
	if store.failed != "" {
		t.Errorf("row error = %q, want none", store.failed)
	}
}

// TestRenditionJobPrepareGates — every refusal arm, each asserting the
// same load-bearing fact: a refused job never transitions the row to
// running, because MarkRunning is the last gate (#309). Row messages
// and verdicts keep renditionRun's choreography.
func TestRenditionJobPrepareGates(t *testing.T) {
	cases := map[string]struct {
		mutate    func(*renditionJob)
		wantRow   string // "" = the row is not written
		permanent bool
		wantErrIs error
	}{
		// A deleted book cascades its rendition row; nothing to record.
		"load failure is permanent and writes no row": {
			mutate: func(j *renditionJob) {
				j.Books = erroringBookReader{err: errors.New("no such book")}
			},
			wantRow:   "",
			permanent: true,
		},
		"non-convertible format is refused with the artifact's message": {
			mutate: func(j *renditionJob) {
				epub := pdfBook()
				epub.Format = "EPUB"
				j.Books = fakeBookReader{book: epub}
			},
			wantRow:   "format EPUB is refused for this artifact",
			permanent: true,
		},
		"config read failure is loud and transient": {
			mutate: func(j *renditionJob) {
				j.Config = func(context.Context) (repo.ConverterConfig, error) {
					return repo.ConverterConfig{}, errors.New("settings table gone")
				}
			},
			wantRow:   "read converter settings: settings table gone",
			permanent: false,
		},
		"not configured is the shared sentinel": {
			mutate: func(j *renditionJob) {
				j.Config = func(context.Context) (repo.ConverterConfig, error) {
					return repo.ConverterConfig{}, nil
				}
			},
			wantRow:   repo.MsgConverterNotConfigured,
			permanent: true,
			wantErrIs: ErrConverterNotConfigured,
		},
		"an unwired artifact seam is refused before running": {
			mutate: func(j *renditionJob) {
				j.Wired = func() (string, error) {
					return "markdown feed is not wired", errors.New("markdown feed is not wired")
				}
			},
			wantRow:   "markdown feed is not wired",
			permanent: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &fakeRenditionStore{}
			job := prepareJob(store)
			tc.mutate(&job)

			_, _, err := job.Prepare(context.Background(), "b1")
			if err == nil {
				t.Fatal("want an error")
			}
			if store.running {
				t.Fatal("a refused job transitioned the row to running")
			}
			if store.failed != tc.wantRow {
				t.Errorf("row error = %q, want %q", store.failed, tc.wantRow)
			}
			if got := errors.Is(err, jobs.ErrDoNotRetry); got != tc.permanent {
				t.Errorf("ErrDoNotRetry = %v, want %v (err %v)", got, tc.permanent, err)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Errorf("err = %v, want errors.Is %v", err, tc.wantErrIs)
			}
		})
	}
}

// TestRenditionJobPrepareMarkRunningFailureRetries — a repo write that
// itself failed is transient and writes no message over its own failure.
func TestRenditionJobPrepareMarkRunningFailureRetries(t *testing.T) {
	store := &fakeRenditionStore{runningErr: errors.New("db down")}
	_, _, err := prepareJob(store).Prepare(context.Background(), "b1")
	if err == nil || errors.Is(err, jobs.ErrDoNotRetry) {
		t.Fatalf("err = %v, want a retryable failure", err)
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("err = %v, want the repo failure surfaced", err)
	}
	if store.failed != "" {
		t.Errorf("row error = %q, want none for a failed row write", store.failed)
	}
}
