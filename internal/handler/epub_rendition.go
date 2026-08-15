// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// renditionEpub is BookFile's second rendition selector, beside
// renditionAudio: a book consumed as its generated EPUB (ADR-0034).
const renditionEpub = "epub"

// epubRenditionStore is the slice of BookEpubRenditionRepo the routes
// need.
type epubRenditionStore interface {
	Start(ctx context.Context, bookID string) error
	GetByBookID(ctx context.Context, bookID string) (model.EpubRendition, error)
}

// newEpubRenditionStore keeps a missing repo nil across the interface
// conversion — same trap as newMarkdownRenditionStore.
func newEpubRenditionStore(r *repo.BookEpubRenditionRepo) epubRenditionStore {
	if r == nil {
		return nil
	}
	return r
}

// epubRenditionDTO is the status answer, mirroring the markdown DTO.
type epubRenditionDTO struct {
	State            string     `json:"state"`
	Error            string     `json:"error,omitempty"`
	ConverterVersion string     `json:"converterVersion,omitempty"`
	Stale            bool       `json:"stale"`
	UpdatedAt        *time.Time `json:"updatedAt,omitempty"`
}

// BookEpubGet answers the generated EPUB's state for the Versions tab —
// the shared status shape with the EPUB's projector.
func (h *Handler) BookEpubGet(c *gin.Context, s bookScope) {
	h.renditionStatus(c, renditionStatusSpec{
		available:      h.epubRenditions != nil,
		unavailableMsg: "generated EPUBs are unavailable",
		readOp:         "read epub rendition",
		load: func(ctx context.Context) (any, error) {
			rendition, err := h.epubRenditions.GetByBookID(ctx, s.Book.ID)
			if err != nil {
				return nil, err
			}
			dto := epubRenditionDTO{
				State:            string(rendition.State),
				Error:            rendition.Error,
				ConverterVersion: rendition.ConverterVersion,
				UpdatedAt:        &rendition.UpdatedAt,
			}
			// A ready row whose file has since gone missing (purged after a
			// scan) is offered as regenerable, not downloadable.
			if rendition.State == model.RenditionReady && rendition.FileID == nil {
				dto.State = "none"
			}
			dto.Stale = h.sourceStale(ctx, s.Book, rendition.State, rendition.SourceContentHash)
			return dto, nil
		},
		none: epubRenditionDTO{State: "none"},
	})
}

// BookEpubGenerate enqueues a render — the shared gate chain with the
// EPUB's configuration. Admin-gated at the route.
func (h *Handler) BookEpubGenerate(c *gin.Context, s bookScope) {
	h.renditionGenerate(c, s, renditionRouteSpec{
		available:         h.epubRenditions != nil && h.epubRequests != nil,
		unavailableMsg:    "generated EPUBs are unavailable",
		notConvertibleMsg: "only " + model.ConvertibleFormatList() + " books can become a generated EPUB",
		requestOp:         "request epub render",
		request:           h.epubRequests.One,
	})
}

// noEpubRenditionMsg is the generated EPUB's one not-found sentence.
const noEpubRenditionMsg = "this book has no generated EPUB"

// serveEpubRendition streams a book's generated EPUB — BookFile's
// ?rendition=epub arm, and one adapter of the shared serve chain:
// the row's ready-and-pointing-at-a-file gate, the files row it points
// at, and the library's own delivery decision.
func (h *Handler) serveEpubRendition(c *gin.Context, book model.Book) {
	h.renditionServe(c, book, renditionServeSpec{
		noneMsg:   noEpubRenditionMsg,
		resolveOp: "epub library handle",
		ready: func(ctx context.Context) (string, bool) {
			// No rendition store wired is the deliberate degrade, and it
			// gets the same answer as a book that has no generated EPUB:
			// this route must not claim otherwise.
			if h.epubRenditions == nil {
				return "", false
			}
			rendition, err := h.epubRenditions.GetByBookID(ctx, book.ID)
			if err != nil || rendition.State != model.RenditionReady || rendition.FileID == nil {
				return "", false
			}
			return *rendition.FileID, true
		},
		locate: renditionFileLocation,
		ext:    ".epub",
		mime:   model.MIMEForFormat("EPUB"),
	})
}
