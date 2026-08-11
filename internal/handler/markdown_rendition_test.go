// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

type fakeRenditions struct {
	row      model.MarkdownRendition
	missing  bool
	started  bool
	coverage repo.ConversionCoverage
}

func (f *fakeRenditions) Start(context.Context, string) error {
	f.started = true
	return nil
}

func (f *fakeRenditions) GetByBookID(context.Context, string) (model.MarkdownRendition, error) {
	if f.missing {
		return model.MarkdownRendition{}, repo.ErrNotFound
	}
	return f.row, nil
}

func (f *fakeRenditions) CountConversionCoverage(context.Context) (repo.ConversionCoverage, error) {
	return f.coverage, nil
}

type captureQueue struct {
	stubQueue
	enqueued []jobs.Args
}

func (q *captureQueue) Enqueue(_ context.Context, a jobs.Args) error {
	q.enqueued = append(q.enqueued, a)
	return nil
}

func pdfScope() bookScope {
	return bookScope{UserID: "u1", Book: model.Book{ID: "b1", Format: "PDF"}}
}

func TestBookMarkdownGetAnswersNoneForAVirginBook(t *testing.T) {
	h := &Handler{renditions: &fakeRenditions{missing: true}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/markdown", "")
	h.BookMarkdownGet(c, pdfScope())

	var got markdownRenditionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if got.State != "none" {
		t.Fatalf("State = %q, want none", got.State)
	}
}

// TestBookMarkdownGetSurfacesTheRowVerbatim — the error string the
// worker recorded is exactly what travels (ADR-0033 §5).
func TestBookMarkdownGetSurfacesTheRowVerbatim(t *testing.T) {
	h := &Handler{renditions: &fakeRenditions{row: model.MarkdownRendition{
		BookID: "b1",
		State:  model.RenditionFailed,
		Error:  "converter extension is not configured",
	}}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/markdown", "")
	h.BookMarkdownGet(c, pdfScope())

	var got markdownRenditionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != "failed" || got.Error != "converter extension is not configured" {
		t.Fatalf("DTO = %+v", got)
	}
}

// TestBookMarkdownGenerateRefusesNonConvertible — EPUB never reaches the
// queue: gap-filler routing (ADR-0033 §2) enforced at the gate.
func TestBookMarkdownGenerateRefusesNonConvertible(t *testing.T) {
	q := &captureQueue{}
	h := &Handler{
		renditions:  &fakeRenditions{},
		appSettings: &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}},
		queue:       q,
	}
	s := pdfScope()
	s.Book.Format = "EPUB"

	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/markdown", "")
	h.BookMarkdownGenerate(c, s)

	if httpStatus(c, rec) != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), CodeFormatNotConvertible) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(q.enqueued) != 0 {
		t.Fatal("an EPUB reached the queue")
	}
}

// TestBookMarkdownGenerateRefusesWhenNotConfigured — the enqueue gate
// answers immediately rather than letting a job fail in thirty seconds.
func TestBookMarkdownGenerateRefusesWhenNotConfigured(t *testing.T) {
	h := &Handler{
		renditions:  &fakeRenditions{},
		appSettings: &fakeAppSettings{},
		queue:       &captureQueue{},
	}
	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/markdown", "")
	h.BookMarkdownGenerate(c, pdfScope())

	if httpStatus(c, rec) != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestBookMarkdownGenerateStartsRowAndEnqueues(t *testing.T) {
	q := &captureQueue{}
	store := &fakeRenditions{}
	h := &Handler{
		renditions:  store,
		appSettings: &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}},
		queue:       q,
	}
	c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/markdown", "")
	h.BookMarkdownGenerate(c, pdfScope())

	if httpStatus(c, rec) != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
	}
	if !store.started {
		t.Fatal("row was not started")
	}
	if len(q.enqueued) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(q.enqueued))
	}
	if _, ok := q.enqueued[0].(jobs.MarkdownRenditionArgs); !ok {
		t.Fatalf("enqueued %T", q.enqueued[0])
	}
}

// --- guide pre-flight over renditions (ADR-0033, #288) -------------------

// TestBookGuideGenerateSurfacesConversionStateAtTheButton — a PDF whose
// conversion already failed, or whose converter is off, is refused at
// enqueue time with the same words the rendition row holds. The guide
// UI shows the server message raw, so the verbatim string is the
// feature.
func TestBookGuideGenerateSurfacesConversionStateAtTheButton(t *testing.T) {
	guideOn := func(f *fakeAppSettings) *fakeAppSettings {
		f.guide = repo.ReadingGuideConfig{Enabled: true, BaseURL: "https://llm/v1", Model: "m"}
		return f
	}

	t.Run("converter not configured", func(t *testing.T) {
		h := &Handler{
			renditions:  &fakeRenditions{missing: true},
			appSettings: guideOn(&fakeAppSettings{}),
			queue:       &captureQueue{},
		}
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
		h.BookGuideGenerate(c, pdfScope())

		if httpStatus(c, rec) != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("conversion failed, verbatim", func(t *testing.T) {
		h := &Handler{
			renditions: &fakeRenditions{row: model.MarkdownRendition{
				State: model.RenditionFailed,
				Error: "PDF has no extractable text (Scanned, 1 pages): OCR is required",
			}},
			appSettings: guideOn(&fakeAppSettings{
				converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"},
			}),
			queue: &captureQueue{},
		}
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
		h.BookGuideGenerate(c, pdfScope())

		if httpStatus(c, rec) != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502 (body %s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "OCR is required") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("missing rendition still enqueues the guide", func(t *testing.T) {
		q := &captureQueue{}
		h := &Handler{
			renditions: &fakeRenditions{missing: true},
			appSettings: guideOn(&fakeAppSettings{
				converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"},
			}),
			queue: q,
		}
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
		h.BookGuideGenerate(c, pdfScope())

		if httpStatus(c, rec) != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		if len(q.enqueued) != 1 {
			t.Fatalf("enqueued %d, want the guide job", len(q.enqueued))
		}
	})

	t.Run("EPUB skips the converter entirely", func(t *testing.T) {
		q := &captureQueue{}
		h := &Handler{
			// No renditions store consulted, no converter config needed:
			// the EPUB path must not depend on either.
			renditions:  &fakeRenditions{missing: true},
			appSettings: guideOn(&fakeAppSettings{}),
			queue:       q,
		}
		s := pdfScope()
		s.Book.Format = "EPUB"
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/guide", "")
		h.BookGuideGenerate(c, s)

		if httpStatus(c, rec) != http.StatusAccepted {
			t.Fatalf("status = %d, want 202 (body %s)", rec.Code, rec.Body.String())
		}
		if len(q.enqueued) != 1 {
			t.Fatalf("enqueued %d, want 1", len(q.enqueued))
		}
	})
}

// --- download (Versions tab) ---------------------------------------------

type fakeLibStore struct{ handle *service.LibraryHandle }

func (f fakeLibStore) For(context.Context, string) (*service.LibraryHandle, error) {
	return f.handle, nil
}

// TestBookMarkdownDownloadStreamsTheRendition — end to end against a
// real local backend: the bytes PlaceMarkdown wrote come back as an
// attachment, resolved through StorageKey (the "/"-rooted local backend
// would miss on the bare location).
func TestBookMarkdownDownloadStreamsTheRendition(t *testing.T) {
	root := t.TempDir()
	rootedAtSlash, err := local.New("/")
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	handle := &service.LibraryHandle{
		Library: model.Library{ID: "lib1", Root: &root},
		Storage: rootedAtSlash,
	}

	book := model.Book{ID: "b1", LibraryID: "lib1", Title: "Dune", Author: "A", Format: "PDF"}
	tmp := filepath.Join(t.TempDir(), "staged.md")
	if err := os.WriteFile(tmp, []byte("# markdown body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	placed, err := handle.PlaceDerived(context.Background(), book, tmp, service.DerivedMarkdown)
	if err != nil {
		t.Fatalf("PlaceDerived: %v", err)
	}

	h := &Handler{
		renditions: &fakeRenditions{row: model.MarkdownRendition{
			BookID: "b1", State: model.RenditionReady, Location: placed.Location,
		}},
		libStore: fakeLibStore{handle: handle},
	}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/markdown/file", "")
	h.BookMarkdownDownload(c, bookScope{UserID: "u1", Book: book})

	if httpStatus(c, rec) != http.StatusOK {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "# markdown body\n" {
		t.Fatalf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Dune.md"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

// TestBookMarkdownDownloadAnswers404ForEveryNonReadyState — the row is
// only offered when ready; a direct URL hit on anything else is
// not-found, not a half-truth.
func TestBookMarkdownDownloadAnswers404ForEveryNonReadyState(t *testing.T) {
	cases := map[string]*fakeRenditions{
		"no row":  {missing: true},
		"failed":  {row: model.MarkdownRendition{State: model.RenditionFailed, Error: "x"}},
		"running": {row: model.MarkdownRendition{State: model.RenditionRunning}},
		"ready but no location": {row: model.MarkdownRendition{
			State: model.RenditionReady,
		}},
	}
	for name, store := range cases {
		t.Run(name, func(t *testing.T) {
			h := &Handler{
				renditions: store,
				libStore:   fakeLibStore{handle: &service.LibraryHandle{}},
			}
			c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/markdown/file", "")
			h.BookMarkdownDownload(c, pdfScope())

			if httpStatus(c, rec) != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
			}
		})
	}
}
