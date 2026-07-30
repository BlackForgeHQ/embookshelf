// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/jobs"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// kindleEmailRegex enforces the public-side shape `*@kindle.com`.
// Amazon's send-to-kindle service accepts only this domain on the
// destination side. ADR-0021. Validation is intentionally cheap —
// real verification happens when Amazon either delivers or bounces.
var kindleEmailRegex = regexp.MustCompile(`^[a-z0-9._-]+@kindle\.com$`)

type kindleEmailReq struct {
	Email string `json:"email"`
}

// AccountKindleEmailUpdate sets or clears the caller's
// users.kindle_email. Empty string clears.
func (h *Handler) AccountKindleEmailUpdate(c *gin.Context) {
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body kindleEmailReq
	if !bindJSON(c, &body) {
		return
	}
	addr := strings.ToLower(strings.TrimSpace(body.Email))
	if addr != "" && !kindleEmailRegex.MatchString(addr) {
		writeError(c, http.StatusBadRequest, "kindle email must look like name@kindle.com")
		return
	}
	if err := h.users.UpdateKindleEmail(c.Request.Context(), u.ID, addr); err != nil {
		writeServerError(c, "account kindle email update", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SendToKindle enqueues a delivery of the book to the caller's
// kindle_email. Synchronous response: 202 once enqueued, 415 for an
// ineligible format, 412 when the kindle_email isn't set, 503 when
// the email subsystem is off or there is no worker pool to dispatch
// through. The actual send happens in task.SendToKindle and reports
// completion via SSE. ADR-0021.
func (h *Handler) SendToKindle(c *gin.Context, s bookScope) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	// The seam has already established the session user; this reaches for
	// the full row because the destination address lives on it.
	u := auth.UserFromContext(c.Request.Context())
	if u == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	if u.KindleEmail == "" {
		// Re-fetch in case the in-context user was loaded before the
		// kindle email was set in this same session.
		fresh, err := h.users.GetByID(c.Request.Context(), u.ID)
		if err == nil && fresh.KindleEmail != "" {
			u = &fresh
		} else {
			writeErrorCode(c, http.StatusPreconditionFailed, CodeKindleEmailUnset,
				"set your Kindle email in account settings first")
			return
		}
	}

	book := s.Book
	if !service.IsKindleEligible(book.Format) {
		writeErrorCode(c, http.StatusUnsupportedMediaType, CodeFormatNotSupported,
			fmt.Sprintf("Send-to-Kindle accepts %s only", model.KindleEligibleFormatList()))
		return
	}

	q, ok := h.requireQueue(c)
	if !ok {
		return
	}
	if err := q.Enqueue(c.Request.Context(), jobs.SendToKindleArgs{BookID: book.ID, UserID: u.ID}); err != nil {
		writeServerError(c, "send to kindle: enqueue", err)
		return
	}
	c.Status(http.StatusAccepted)
}
