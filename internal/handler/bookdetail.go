// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// bookDetailDTO adds the user's shelf memberships to the base book shape.
type bookDetailDTO struct {
	bookDTO
	Shelves []string `json:"shelves"`
}

// writeBookDetail answers with the current wire representation of book
// bookID for user userID.
//
// One module, five callers: the book read, the metadata PATCH, apply
// match, the field-lock toggle and bookdrop approve. Each of them used to
// assemble this by hand — reload the row, fetch the shelf slugs, turn a
// nil slice into an empty one, build the payload, and at three of the
// five attach the warnings — and they had already drifted. Approve
// hard-coded an empty shelf list instead of querying, so a book approved
// onto a shelf came back claiming it was [[Unshelved]]; two of the five
// carried no warnings at all.
//
// Three rules live here rather than at the call sites:
//
//   - Reload. Four of the five callers have just written to the row, and
//     the response has to carry what the write produced rather than what
//     the caller had in hand. Uniform rather than conditional: the read
//     pays one extra primary-key lookup, which is the price of the five
//     sites having one answer instead of five.
//   - Nil to empty. A nil slice marshals to JSON null, and the client's
//     Book type declares shelves as string[].
//   - Warnings. A degraded write still saved the edit, and only the
//     person who made it can act on the part that did not land.
//
// Callers that cannot degrade — the read and approve, which routes around
// MetadataWriter by design (ADR-0001 §3) — pass the zero Outcome and an
// empty log message.
func (h *Handler) writeBookDetail(
	c *gin.Context, userID, bookID string, out service.Outcome, logMsg string,
) {
	ctx := c.Request.Context()

	book, err := h.books.GetByID(ctx, userID, bookID)
	if err != nil {
		// A book that vanished between the write and the reload is a 404
		// rather than a 500: the request did what it was asked, and the
		// row is genuinely not there to describe.
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "book detail reload", err)
		return
	}

	shelves, err := h.shelf.SlugsForBook(ctx, userID, bookID)
	if err != nil {
		writeServerError(c, "book detail shelves", err)
		return
	}
	if shelves == nil {
		shelves = []string{}
	}

	body := gin.H{
		"book": bookDetailDTO{
			bookDTO: toBookDTO(book),
			Shelves: shelves,
		},
	}
	attachWarnings(body, out, logMsg, bookID)
	c.JSON(http.StatusOK, body)
}

// attachWarnings puts a degraded write's warnings on the response body.
//
// A degraded write still saved the edit — the books row is canonical —
// but the Sidecar or the in-file copy did not keep up, and only the
// person who made the edit can act on that. Reporting an unqualified
// success they cannot act on is the failure mode this exists to prevent.
//
// All three edit endpoints — metadata PATCH, field-lock toggle, apply
// match — reach it through writeBookDetail, so a client parses one shape
// whichever it called: a top-level "warnings" array of strings alongside
// "book", present only when a step actually failed. Apply match used to
// be missing from that list, because ApplyMatch discarded the Outcome
// before the handler could see it.
func attachWarnings(body gin.H, out service.Outcome, logMsg, bookID string) {
	warnings := out.Warnings()
	if len(warnings) == 0 {
		return
	}
	slog.Warn(logMsg, "book", bookID, "warnings", warnings)
	body["warnings"] = warnings
}
