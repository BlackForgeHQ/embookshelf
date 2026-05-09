// SPDX-License-Identifier: AGPL-3.0-or-later

package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

// resetRateLimit is the per-user "1 reset email per 5 minutes" guard.
// Counted server-side from password_reset_tokens.created_at so it
// survives a process restart. ADR-0020.
const resetRateLimit = 5 * time.Minute

type passwordResetRequestReq struct {
	Email string `json:"email"`
}

// PasswordResetRequest issues a reset email if the address belongs
// to a user. The response is identical regardless of whether the
// email exists, preventing account enumeration. ADR-0020.
func (h *Handler) PasswordResetRequest(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	var body passwordResetRequestReq
	if !bindJSON(c, &body) {
		return
	}
	body.Email = normalizeEmail(body.Email)

	// Always 202. Treat every failure mode (no user, rate limit,
	// transient SMTP) as success on the wire so a guesser learns
	// nothing. Real failures land in logs.
	defer c.Status(http.StatusAccepted)

	if body.Email == "" {
		return
	}
	ctx := c.Request.Context()
	user, err := h.users.GetByEmail(ctx, body.Email)
	if err != nil {
		if !errors.Is(err, repo.ErrNotFound) {
			slog.Warn("password reset: user lookup", "err", err)
		}
		return
	}
	// User has no local password (OIDC-only) — issuing a reset would
	// turn into "set initial password" which is its own flow. Skip
	// silently to keep the response opaque.
	if user.PasswordHash == "" {
		return
	}

	// Per-user rate limit: at most one in-flight token per 5 min.
	count, err := h.resetRepo.CountRecentForUser(ctx, user.ID, time.Now().Add(-resetRateLimit))
	if err != nil {
		slog.Warn("password reset: rate count", "err", err)
		return
	}
	if count > 0 {
		return
	}

	if err := h.notifier.IssuePasswordReset(ctx, user); err != nil {
		slog.Warn("password reset: issue", "err", err, "user_id", user.ID)
	}
}

type passwordResetVerifyResp struct {
	Valid     bool   `json:"valid"`
	Email     string `json:"email,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// PasswordResetVerify is the pre-flight the UI calls to decide
// whether to render the new-password form vs an "expired" page. Read
// only — does not consume the token. Constant-time look-ups not
// required because the row is keyed by sha256 hash, but we still
// return identical 200 JSON for valid/invalid to avoid leaking
// status via response shape.
func (h *Handler) PasswordResetVerify(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	plain := strings.TrimSpace(c.Query("token"))
	if plain == "" {
		c.JSON(http.StatusOK, passwordResetVerifyResp{Valid: false})
		return
	}
	hash := service.HashToken(plain)
	row, err := h.resetRepo.Verify(c.Request.Context(), hash, time.Now())
	if err != nil {
		c.JSON(http.StatusOK, passwordResetVerifyResp{Valid: false})
		return
	}
	user, err := h.users.GetByID(c.Request.Context(), row.UserID)
	if err != nil {
		c.JSON(http.StatusOK, passwordResetVerifyResp{Valid: false})
		return
	}
	c.JSON(http.StatusOK, passwordResetVerifyResp{
		Valid:     true,
		Email:     user.Email,
		ExpiresAt: row.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

type passwordResetConfirmReq struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// PasswordResetConfirm consumes the token, hashes the new password,
// and writes it. The token-consume + password-write pair is two
// statements because the new hash needs to be computed bcrypt-side
// first. The Consume step is atomic so a concurrent request can't
// land twice.
func (h *Handler) PasswordResetConfirm(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	var body passwordResetConfirmReq
	if !bindJSON(c, &body) {
		return
	}
	plain := strings.TrimSpace(body.Token)
	if plain == "" || body.NewPassword == "" {
		writeError(c, http.StatusBadRequest, "token and newPassword are required")
		return
	}
	hash := service.HashToken(plain)
	row, err := h.resetRepo.Consume(c.Request.Context(), hash, time.Now())
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusBadRequest, "reset link is invalid or expired")
			return
		}
		writeServerError(c, "password reset confirm", err)
		return
	}
	pwHash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
			return
		}
		writeServerError(c, "password reset hash", err)
		return
	}
	if err := h.users.UpdatePassword(c.Request.Context(), row.UserID, pwHash); err != nil {
		writeServerError(c, "password reset write", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func writeEmailDisabled(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": gin.H{
			"code":    "EMAIL_DISABLED",
			"message": "email delivery is not configured by the admin",
		},
	})
}
