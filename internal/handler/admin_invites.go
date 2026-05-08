package handler

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

type inviteCreateReq struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type inviteDTO struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	InvitedByID   string `json:"invitedById"`
	InvitedByName string `json:"invitedByName,omitempty"`
	CreatedAt     string `json:"createdAt"`
	ExpiresAt     string `json:"expiresAt"`
}

// AdminInviteCreate issues a fresh invite. Admin-only; the inviter's
// id is recorded so the audit trail survives a delete of the inviter
// (FK is ON DELETE CASCADE — the row goes with them).
func (h *Handler) AdminInviteCreate(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	inviter := auth.UserFromContext(c.Request.Context())
	if inviter == nil {
		writeError(c, http.StatusUnauthorized, "authentication required")
		return
	}
	var body inviteCreateReq
	if !bindJSON(c, &body) {
		return
	}
	emailAddr := normalizeEmail(body.Email)
	if emailAddr == "" {
		writeError(c, http.StatusBadRequest, "email is required")
		return
	}
	if _, err := mail.ParseAddress(emailAddr); err != nil {
		writeError(c, http.StatusBadRequest, "invalid email address")
		return
	}
	role := model.Role(strings.TrimSpace(body.Role))
	if role != model.RoleAdmin && role != model.RoleUser {
		role = model.RoleUser
	}

	if _, err := h.notifier.IssueAdminInvite(c.Request.Context(), emailAddr, role, *inviter); err != nil {
		if errors.Is(err, service.ErrEmailDisabled) {
			writeEmailDisabled(c)
			return
		}
		writeServerError(c, "admin invite create", err)
		return
	}
	c.Status(http.StatusCreated)
}

// AdminInvitesList returns pending invites. Token hashes are
// rendered hex-encoded so the admin UI can use them as opaque
// identifiers in the revoke call without exposing the plaintext
// (which is gone).
func (h *Handler) AdminInvitesList(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	rows, err := h.inviteRepo.ListPending(c.Request.Context(), time.Now())
	if err != nil {
		writeServerError(c, "admin invites list", err)
		return
	}
	out := make([]inviteDTO, 0, len(rows))
	inviterCache := map[string]string{}
	for _, r := range rows {
		dto := inviteDTO{
			ID:          hex.EncodeToString(r.TokenHash),
			Email:       r.Email,
			Role:        string(r.Role),
			InvitedByID: r.InvitedBy,
			CreatedAt:   r.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:   r.ExpiresAt.UTC().Format(time.RFC3339),
		}
		if name, ok := inviterCache[r.InvitedBy]; ok {
			dto.InvitedByName = name
		} else if u, err := h.users.GetByID(c.Request.Context(), r.InvitedBy); err == nil {
			inviterCache[r.InvitedBy] = u.Display()
			dto.InvitedByName = u.Display()
		}
		out = append(out, dto)
	}
	c.JSON(http.StatusOK, gin.H{"invites": out})
}

// AdminInviteRevoke deletes a pending invite by token-hash hex id.
// Idempotent — silent 204 even when the row is already gone so the
// admin UI doesn't surface a misleading "missing" toast on a double
// click.
func (h *Handler) AdminInviteRevoke(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	hash, err := hex.DecodeString(c.Param("id"))
	if err != nil || len(hash) == 0 {
		writeError(c, http.StatusBadRequest, "invalid invite id")
		return
	}
	if err := h.inviteRepo.Revoke(c.Request.Context(), hash); err != nil {
		writeServerError(c, "admin invite revoke", err)
		return
	}
	c.Status(http.StatusNoContent)
}

type inviteAcceptReq struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// InviteAccept materialises the user from a valid invite. Public
// endpoint — no session needed yet. Marks the invite consumed in the
// same statement it scopes to "still valid" so a tab-double-submit
// can't create two rows.
func (h *Handler) InviteAccept(c *gin.Context) {
	if !h.emailEnabled() {
		writeEmailDisabled(c)
		return
	}
	var body inviteAcceptReq
	if !bindJSON(c, &body) {
		return
	}
	plain := strings.TrimSpace(body.Token)
	if plain == "" || body.Password == "" {
		writeError(c, http.StatusBadRequest, "token and password are required")
		return
	}
	hash := service.HashToken(plain)
	now := time.Now()
	row, err := h.inviteRepo.GetByHash(c.Request.Context(), hash, now)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			writeError(c, http.StatusGone, "invite is invalid or expired")
			return
		}
		writeServerError(c, "invite lookup", err)
		return
	}
	pwHash, err := auth.HashPassword(body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrWeakPassword) {
			writeError(c, http.StatusBadRequest, auth.ErrWeakPassword.Error())
			return
		}
		writeServerError(c, "invite hash", err)
		return
	}
	user, err := h.users.Create(c.Request.Context(), row.Email, strings.TrimSpace(body.Name), pwHash, row.Role)
	if err != nil {
		writeServerError(c, "invite create user", err)
		return
	}
	if err := h.inviteRepo.MarkAccepted(c.Request.Context(), hash, user.ID, now); err != nil {
		// User row exists but the invite seal failed — log and continue.
		// The next sweeper will purge the expired row.
		writeServerError(c, "invite mark accepted", err)
		return
	}
	sess, err := h.auth.IssueSession(c.Request.Context(), user.ID, c.Request.UserAgent())
	if err != nil {
		writeServerError(c, "invite issue session", err)
		return
	}
	auth.SetSessionCookie(c, sess.ID, service.SessionTTL, h.Secure())
	c.JSON(http.StatusOK, gin.H{"user": toUserDTO(user)})
}
