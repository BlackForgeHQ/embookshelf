// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import "github.com/gin-gonic/gin"

// userHandler is a handler body that starts from a resolved session user.
//
// The user id arrives as an argument rather than something the body fetches
// for itself, on purpose: a body cannot run without one, so the seam fails
// closed by construction instead of by convention — the user-axis twin of
// bookHandler.
type userHandler func(*gin.Context, string)

// userScoped is the user-scoped seam: the one place that resolves the
// session user and hands the body its id.
//
// It exists because that preamble — `userID := requireUserID(c); if
// userID == "" { return }` — was restated at 25 call sites across nine
// files, the same drift class bookScoped closed off for the book axis
// (#241). The failure mode here is milder: auth middleware sits upstream of
// every route, so a forgotten check nil-derefs on a missing user rather than
// leaking another user's data. Milder is why this seam is smaller than
// bookScoped's, not why it's unnecessary — 25 restatements of one idiom is
// still 25 places that can drift.
func (h *Handler) userScoped(fn userHandler) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := requireUserID(c)
		if userID == "" {
			return
		}
		fn(c, userID)
	}
}
