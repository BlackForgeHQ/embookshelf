// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// ResetRateLimit is the per-user "one reset email per 5 minutes" guard.
// Counted server-side from password_reset_tokens.created_at so it survives a
// process restart. ADR-0020.
const ResetRateLimit = 5 * time.Minute

// ErrResetTokenInvalid is returned whenever a reset token cannot be used —
// unknown, expired, or already spent. The three are deliberately
// indistinguishable so a brute-force attempt learns nothing from the reply.
var ErrResetTokenInvalid = errors.New("reset link is invalid or expired")

// passwordResetUsers is the slice of UserRepo this service touches.
type passwordResetUsers interface {
	GetByEmail(ctx context.Context, email string) (model.User, error)
	GetByID(ctx context.Context, id string) (model.User, error)
	UpdatePassword(ctx context.Context, id, hash string) error
}

// passwordResetTokens is the slice of PasswordResetTokenRepo this service
// touches. Issuance lives on the notifier, not here, because the token's
// plaintext must never leave the code that mails it.
type passwordResetTokens interface {
	CountRecentForUser(ctx context.Context, userID string, since time.Time) (int, error)
	Verify(ctx context.Context, hash []byte, now time.Time) (repo.PasswordResetToken, error)
	Consume(ctx context.Context, hash []byte, now time.Time) (repo.PasswordResetToken, error)
}

// sessionEvictor drops every session belonging to a user. Implemented by
// *repo.SessionRepo.
type sessionEvictor interface {
	DeleteForUser(ctx context.Context, userID string) (int64, error)
}

// resetIssuer mints a token and mails the link. Implemented by *Notifier.
type resetIssuer interface {
	IssuePasswordReset(ctx context.Context, user model.User) error
}

// PasswordResetService owns the reset lifecycle: who may request a link, what
// a link proves, and what consuming one does.
//
// It sits behind narrow seams so the whole lifecycle — including the orderings
// that matter, like "validate the new password before spending the token" —
// is testable without a database or an SMTP server.
type PasswordResetService struct {
	users    passwordResetUsers
	tokens   passwordResetTokens
	sessions sessionEvictor
	issuer   resetIssuer
	now      func() time.Time
}

func NewPasswordResetService(
	users passwordResetUsers,
	tokens passwordResetTokens,
	sessions sessionEvictor,
	issuer resetIssuer,
) *PasswordResetService {
	return &PasswordResetService{
		users:    users,
		tokens:   tokens,
		sessions: sessions,
		issuer:   issuer,
		now:      time.Now,
	}
}

// Request mails a reset link if the address belongs to a user who can use
// one.
//
// The returned error is for the caller's log only and must never reach the
// wire: the HTTP layer answers 202 for every outcome so that a guesser cannot
// tell a registered address from an unregistered one (ADR-0020). Skipped
// requests — unknown address, no local password, rate limited — return nil,
// because nothing went wrong.
func (s *PasswordResetService) Request(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	// No local password (OIDC-only account) — a reset here would quietly
	// become "set an initial password", which is a different flow with
	// different authorisation. Skip.
	if user.PasswordHash == "" {
		return nil
	}
	count, err := s.tokens.CountRecentForUser(ctx, user.ID, s.now().Add(-ResetRateLimit))
	if err != nil {
		return fmt.Errorf("rate count: %w", err)
	}
	if count > 0 {
		return nil
	}
	if err := s.issuer.IssuePasswordReset(ctx, user); err != nil {
		return fmt.Errorf("issue reset: %w", err)
	}
	return nil
}

// ResetTarget describes a live reset link: whose it is and when it dies.
type ResetTarget struct {
	Email     string
	ExpiresAt time.Time
}

// Verify reports whether a token is still usable, without spending it. The UI
// pre-flights with this to decide between rendering the new-password form and
// an "expired link" page, so it must stay read-only — the page can be
// rendered any number of times before the user submits.
//
// Returns ok=false for every failure; callers must not distinguish them on
// the wire.
func (s *PasswordResetService) Verify(ctx context.Context, plainToken string) (ResetTarget, bool) {
	plain := strings.TrimSpace(plainToken)
	if plain == "" {
		return ResetTarget{}, false
	}
	row, err := s.tokens.Verify(ctx, HashToken(plain), s.now())
	if err != nil {
		return ResetTarget{}, false
	}
	user, err := s.users.GetByID(ctx, row.UserID)
	if err != nil {
		return ResetTarget{}, false
	}
	return ResetTarget{Email: user.Email, ExpiresAt: row.ExpiresAt}, true
}

// Confirm spends a reset token and installs a new password.
//
// The step order is load-bearing:
//
//  1. hash the new password first, so a rejected password (too short, too
//     common) costs the user nothing — the link still works on the retry.
//     Doing this after Consume left the user with an error *and* a dead link,
//     unable to ask for another until the 5-minute rate limit lapsed.
//  2. Consume, which is a single atomic statement, so a double-submit can
//     only land once.
//  3. write the password.
//  4. evict the user's sessions. A reset is the remedy for a compromised
//     account, so any session an intruder already holds has to die with the
//     old password. This runs last and its failure is logged rather than
//     returned: the new password is already live, and reporting failure would
//     tell the user their reset did not work when it did.
//
// Returns ErrResetTokenInvalid for an unusable token and auth.ErrWeakPassword
// for a password that fails policy.
func (s *PasswordResetService) Confirm(ctx context.Context, plainToken, newPassword string) error {
	plain := strings.TrimSpace(plainToken)
	if plain == "" {
		return ErrResetTokenInvalid
	}
	pwHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	row, err := s.tokens.Consume(ctx, HashToken(plain), s.now())
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return ErrResetTokenInvalid
		}
		return fmt.Errorf("consume reset token: %w", err)
	}
	if err := s.users.UpdatePassword(ctx, row.UserID, pwHash); err != nil {
		return fmt.Errorf("write password: %w", err)
	}
	evictSessions(ctx, s.sessions, row.UserID, "password reset")
	return nil
}

// evictSessions drops every session for a user after their password changes,
// logging rather than returning a failure — the password is already written,
// and the caller has nothing useful to do with the error.
func evictSessions(ctx context.Context, sessions sessionEvictor, userID, reason string) {
	if sessions == nil {
		return
	}
	if _, err := sessions.DeleteForUser(ctx, userID); err != nil {
		slog.Warn("evict sessions after password change",
			"err", err, "user_id", userID, "reason", reason)
	}
}
