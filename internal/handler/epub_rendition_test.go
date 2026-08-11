// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

type fakeEpubRenditions struct {
	row     model.EpubRendition
	missing bool
	started bool
}

func (f *fakeEpubRenditions) Start(context.Context, string) error {
	f.started = true
	return nil
}

func (f *fakeEpubRenditions) GetByBookID(context.Context, string) (model.EpubRendition, error) {
	if f.missing {
		return model.EpubRendition{}, repo.ErrNotFound
	}
	return f.row, nil
}

func TestBookEpubGetAnswersNoneForAVirginBook(t *testing.T) {
	h := &Handler{epubRenditions: &fakeEpubRenditions{missing: true}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/epub", "")
	h.BookEpubGet(c, pdfScope())

	var got epubRenditionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if got.State != "none" {
		t.Fatalf("State = %q, want none", got.State)
	}
}

// TestBookEpubGetReadyWithoutFileReadsAsNone — the file was purged
// after going missing; the row must offer regeneration, not a download
// that 404s.
func TestBookEpubGetReadyWithoutFileReadsAsNone(t *testing.T) {
	h := &Handler{epubRenditions: &fakeEpubRenditions{row: model.EpubRendition{
		State: model.RenditionReady, FileID: nil,
	}}}
	c, rec := settingsCtx(t, http.MethodGet, "/api/v1/books/b1/epub", "")
	h.BookEpubGet(c, pdfScope())

	var got epubRenditionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != "none" {
		t.Fatalf("State = %q, want none for a ready row with no file", got.State)
	}
}

func TestBookEpubGenerateGatesMatchTheMarkdownButton(t *testing.T) {
	t.Run("EPUB refused", func(t *testing.T) {
		q := &captureQueue{}
		h := &Handler{
			epubRenditions: &fakeEpubRenditions{},
			appSettings:    &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}},
			queue:          q,
		}
		s := pdfScope()
		s.Book.Format = "EPUB"
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/epub", "")
		h.BookEpubGenerate(c, s)

		if httpStatus(c, rec) != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		if len(q.enqueued) != 0 {
			t.Fatal("an EPUB reached the queue")
		}
	})

	t.Run("not configured refused verbatim", func(t *testing.T) {
		h := &Handler{
			epubRenditions: &fakeEpubRenditions{},
			appSettings:    &fakeAppSettings{},
			queue:          &captureQueue{},
		}
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/epub", "")
		h.BookEpubGenerate(c, pdfScope())

		if httpStatus(c, rec) != http.StatusServiceUnavailable {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("happy path starts row and enqueues", func(t *testing.T) {
		q := &captureQueue{}
		store := &fakeEpubRenditions{}
		h := &Handler{
			epubRenditions: store,
			appSettings:    &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}},
			queue:          q,
		}
		c, rec := settingsCtx(t, http.MethodPost, "/api/v1/books/b1/epub", "")
		h.BookEpubGenerate(c, pdfScope())

		if httpStatus(c, rec) != http.StatusAccepted {
			t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
		}
		if !store.started || len(q.enqueued) != 1 {
			t.Fatalf("started = %v, enqueued = %d", store.started, len(q.enqueued))
		}
		if _, ok := q.enqueued[0].(jobs.EpubRenderArgs); !ok {
			t.Fatalf("enqueued %T", q.enqueued[0])
		}
	})
}
