// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// renditionRouteCase adapts one artifact's generate route to the shared
// gate suite: how to build a Handler around the fakes, how to invoke
// the route, and what a success must have enqueued.
type renditionRouteCase struct {
	// build returns a Handler whose artifact store is wired (or nil when
	// withStore is false) plus probes for the started row and the queue.
	build   func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool)
	invoke  func(h *Handler, c *gin.Context, s bookScope)
	jobType func(a jobs.Args) bool
}

// TestRenditionGenerateGateChain — the one gate-order suite for both
// generate buttons (#303): nil store → Convertible → requireConverter →
// requireQueue → Start+Enqueue+202. Ran per artifact, so the two routes
// cannot drift the way the deleted
// TestBookEpubGenerateGatesMatchTheMarkdownButton confessed they could.
func TestRenditionGenerateGateChain(t *testing.T) {
	configured := func() *fakeAppSettings {
		return &fakeAppSettings{converter: repo.ConverterConfig{Enabled: true, BaseURL: "http://c"}}
	}

	artifacts := map[string]renditionRouteCase{
		"markdown": {
			build: func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool) {
				store := &fakeRenditions{missing: true}
				h := &Handler{appSettings: settings, queue: q}
				if withStore {
					h.renditions = store
					h.mdRequests = service.NewMarkdownRequests(store, q)
				}
				return h, func() bool { return store.started }
			},
			invoke:  func(h *Handler, c *gin.Context, s bookScope) { h.BookMarkdownGenerate(c, s) },
			jobType: func(a jobs.Args) bool { _, ok := a.(jobs.MarkdownRenditionArgs); return ok },
		},
		"epub": {
			build: func(withStore bool, settings *fakeAppSettings, q *captureQueue) (*Handler, func() bool) {
				store := &fakeEpubRenditions{missing: true}
				h := &Handler{appSettings: settings, queue: q}
				if withStore {
					h.epubRenditions = store
					h.epubRequests = service.NewEpubRequests(store, q)
				}
				return h, func() bool { return store.started }
			},
			invoke:  func(h *Handler, c *gin.Context, s bookScope) { h.BookEpubGenerate(c, s) },
			jobType: func(a jobs.Args) bool { _, ok := a.(jobs.EpubRenderArgs); return ok },
		},
	}

	for name, art := range artifacts {
		t.Run(name, func(t *testing.T) {
			t.Run("nil store answers 503", func(t *testing.T) {
				q := &captureQueue{}
				h, _ := art.build(false, configured(), q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
			})

			t.Run("non-convertible refused before any gate spends work", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, configured(), q)
				s := pdfScope()
				s.Book.Format = "EPUB"
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, s)
				if httpStatus(c, rec) != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if started() || len(q.enqueued) != 0 {
					t.Fatal("a non-convertible book reached the row or the queue")
				}
			})

			t.Run("not configured refused verbatim", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, &fakeAppSettings{}, q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if !strings.Contains(rec.Body.String(), "converter extension is not configured") {
					t.Fatalf("body = %s", rec.Body.String())
				}
				if started() || len(q.enqueued) != 0 {
					t.Fatal("an unconfigured converter reached the row or the queue")
				}
			})

			t.Run("no queue answers 503 after the converter gate", func(t *testing.T) {
				h, started := art.build(true, configured(), nil)
				h.queue = nil
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusServiceUnavailable {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if started() {
					t.Fatal("the row went pending with no queue to work it")
				}
			})

			t.Run("happy path starts the row then enqueues", func(t *testing.T) {
				q := &captureQueue{}
				h, started := art.build(true, configured(), q)
				c, rec := settingsCtx(t, http.MethodPost, "/x", "")
				art.invoke(h, c, pdfScope())
				if httpStatus(c, rec) != http.StatusAccepted {
					t.Fatalf("status = %d (body %s)", rec.Code, rec.Body.String())
				}
				if !started() || len(q.enqueued) != 1 || !art.jobType(q.enqueued[0]) {
					t.Fatalf("started = %v, enqueued = %+v", started(), q.enqueued)
				}
			})
		})
	}
}
