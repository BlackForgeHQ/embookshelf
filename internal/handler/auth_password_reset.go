// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/service"
)

type passwordResetRequestReq struct {
	Email string `json:"email"`
}

// PasswordResetRequest issues a reset email if the address belongs to a user.
// The response is identical regardless of whether the email exists,
// preventing account enumeration. ADR-0020.
func (h *Handler) PasswordResetRequest(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	var body passwordResetRequestReq
	if !bindJSON(c, &body) {
		return
	}

	// Always 202. Every failure mode — no such user, rate limited, transient
	// SMTP — looks like success on the wire so a guesser learns nothing.
	// Real failures land in the log.
	if err := h.resets.Request(c.Request.Context(), normalizeEmail(body.Email)); err != nil {
		slog.Warn("password reset request", "err", err)
	}
	c.Status(http.StatusAccepted)
}

type passwordResetVerifyResp struct {
	Valid     bool   `json:"valid"`
	Email     string `json:"email,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// PasswordResetVerify is the pre-flight the UI calls to decide whether to
// render the new-password form or an "expired link" page. Read-only — it does
// not spend the token. Valid and invalid both answer 200 with the same JSON
// shape so status cannot be inferred from the response.
func (h *Handler) PasswordResetVerify(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	target, ok := h.resets.Verify(c.Request.Context(), c.Query("token"))
	if !ok {
		c.JSON(http.StatusOK, passwordResetVerifyResp{Valid: false})
		return
	}
	c.JSON(http.StatusOK, passwordResetVerifyResp{
		Valid:     true,
		Email:     target.Email,
		ExpiresAt: target.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

type passwordResetConfirmReq struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// PasswordResetConfirm spends the token and installs the new password. A
// password that fails policy is a 400 and leaves the link usable, so the user
// can simply retry.
func (h *Handler) PasswordResetConfirm(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	var body passwordResetConfirmReq
	if !bindJSON(c, &body) {
		return
	}
	err := h.resets.Confirm(c.Request.Context(), body.Token, body.NewPassword)
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, service.ErrResetTokenInvalid):
		writeError(c, http.StatusBadRequest, service.ErrResetTokenInvalid.Error())
	case errors.Is(err, auth.ErrWeakPassword):
		writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
	default:
		writeServerError(c, "password reset confirm", err)
	}
}

func writeEmailDisabled(c *gin.Context) {
	writeErrorCode(c, http.StatusServiceUnavailable, CodeEmailDisabled,
		"email delivery is not configured by the admin")
}
