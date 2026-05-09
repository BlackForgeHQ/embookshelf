// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/repo"
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
// the email subsystem is off. The actual send happens in
// task.SendToKindle and reports completion via SSE. ADR-0021.
func (h *Handler) SendToKindle(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
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
			c.JSON(http.StatusPreconditionFailed, gin.H{
				"error": gin.H{"code": "KINDLE_EMAIL_UNSET", "message": "set your Kindle email in account settings first"},
			})
			return
		}
	}

	bookID := c.Param("id")
	book, err := h.books.GetByID(c.Request.Context(), u.ID, bookID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusNotFound, "book not found")
			return
		}
		writeServerError(c, "send to kindle: load book", err)
		return
	}
	if !service.IsKindleEligible(book.Format) {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{
			"error": gin.H{"code": "FORMAT_NOT_SUPPORTED", "message": "Send-to-Kindle accepts EPUB and PDF only"},
		})
		return
	}

	if err := h.queue.EnqueueSendToKindle(c.Request.Context(), book.ID, u.ID); err != nil {
		writeServerError(c, "send to kindle: enqueue", err)
		return
	}
	c.Status(http.StatusAccepted)
}
