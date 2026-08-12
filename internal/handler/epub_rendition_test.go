// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

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

func (f *fakeEpubRenditions) MarkFailed(context.Context, string, string) error { return nil }

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

// The generate gate chain lives in TestRenditionGenerateGateChain,
// run over both artifacts — the per-artifact copy this file carried
// (TestBookEpubGenerateGatesMatchTheMarkdownButton) is gone with it.
