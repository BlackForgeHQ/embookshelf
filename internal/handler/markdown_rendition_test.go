// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

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

func (f *fakeRenditions) MarkFailed(context.Context, string, string) error { return nil }

func (f *fakeRenditions) ListConversionCandidates(context.Context) ([]repo.ConversionCandidate, error) {
	return nil, nil
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
// The generate gate chain (nil store, non-convertible, not-configured,
// no queue, happy path) is TestRenditionGenerateGateChain's suite,
// run over both artifacts.

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
			guides:      &fakeReadingGuideStore{missing: true},
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
			guides: &fakeReadingGuideStore{missing: true},
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
			guides:     &fakeReadingGuideStore{missing: true},
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
			guides: &fakeReadingGuideStore{missing: true},
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

// TestBookMarkdownDownloadGateChain — the markdown download route's own
// gate order, and it is its own: the seam missing answers 503 because
// this route *is* the feature, where the two ?rendition= arms answer
// 404 (TestRenditionServeGateChain). Every state the Versions tab does
// not offer a download for is a not-found with the one sentence, and
// the ready one comes back as an attachment — always, without being
// asked, because the route is a download route.
//
// Subsumes the two tests this file carried before (#316): the streams
// case is the "ready" subtest here, over the same real local backend,
// and the non-ready cases are the refusals, now asserting the sentence
// as well as the status.
func TestBookMarkdownDownloadGateChain(t *testing.T) {
	const noneMsg = "this book has no markdown rendition"
	invoke := func(t *testing.T, h *Handler, book model.Book) (*gin.Context, *httptest.ResponseRecorder) {
		t.Helper()
		c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/markdown/file", "")
		h.BookMarkdownDownload(c, bookScope{UserID: "u1", Book: book})
		return c, rec
	}

	t.Run("an unwired seam is a 503, not a 404", func(t *testing.T) {
		h := &Handler{libStore: fakeLibStore{handle: &service.LibraryHandle{}}}
		c, rec := invoke(t, h, model.Book{ID: "b1", Title: "Dune"})

		if httpStatus(c, rec) != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "markdown renditions are unavailable") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	// The row is only offered when ready; a direct URL hit on anything
	// else is not-found, not a half-truth.
	refused := map[string]*fakeRenditions{
		"no row":                {missing: true},
		"failed":                {row: model.MarkdownRendition{State: model.RenditionFailed, Error: "x"}},
		"running":               {row: model.MarkdownRendition{State: model.RenditionRunning}},
		"ready but no location": {row: model.MarkdownRendition{State: model.RenditionReady}},
	}
	for name, store := range refused {
		t.Run(name+" is not found", func(t *testing.T) {
			h := &Handler{
				renditions: store,
				libStore:   fakeLibStore{handle: &service.LibraryHandle{}},
			}
			c, rec := invoke(t, h, pdfScope().Book)

			if httpStatus(c, rec) != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), noneMsg) {
				t.Fatalf("body = %s, want %q", rec.Body.String(), noneMsg)
			}
		})
	}

	// End to end against a real local backend: the bytes PlaceDerived
	// wrote come back as an attachment, resolved through StorageKey (the
	// "/"-rooted local backend would miss on the bare location).
	t.Run("ready serves the bytes as an attachment", func(t *testing.T) {
		h, book := markdownServed(t)
		c, rec := invoke(t, h, book)

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
	})
}

// markdownServed is that library: one book whose markdown rendition is
// really on disk, behind a "/"-rooted local backend.
func markdownServed(t *testing.T) (*Handler, model.Book) {
	t.Helper()
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
	if err := os.WriteFile(tmp, []byte("# markdown body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	placed, err := handle.PlaceDerived(context.Background(), book, tmp, service.DerivedMarkdown)
	if err != nil {
		t.Fatalf("PlaceDerived: %v", err)
	}
	return &Handler{
		renditions: &fakeRenditions{row: model.MarkdownRendition{
			BookID: book.ID, State: model.RenditionReady, Location: placed.Location,
		}},
		libStore: fakeLibStore{handle: handle},
	}, book
}
