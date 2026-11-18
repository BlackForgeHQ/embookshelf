// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// APITokenPrefix marks an embookshelf personal access token, so secret
// scanners and humans can recognise one (ADR-0039 §2).
const APITokenPrefix = "ebk_"

// apiTokenDisplayChars is how much of a token after APITokenPrefix is
// stored for display.
const apiTokenDisplayChars = 8

// MaxAPITokenNameLen bounds a token's name. It is a label, not a field
// anyone should need more for.
const MaxAPITokenNameLen = 100

// ErrMCPDisabled is the refusal while the instance's MCP switch is off.
// Handlers answer it as 503 MCP_DISABLED.
var ErrMCPDisabled = errors.New("MCP is not enabled on this instance")

// ErrAPITokenName is a missing or over-long token name.
var ErrAPITokenName = fmt.Errorf("token name is required and must be at most %d characters", MaxAPITokenNameLen)

// ErrInvalidAPIToken covers every way a presented token fails to
// authenticate — malformed, unknown, expired, revoked, or owned by a user
// who may not sign in. One error, so a caller cannot leak which. It is the
// auth package's sentinel, so the bearer middleware can tell a refusal
// from an outage.
var ErrInvalidAPIToken = auth.ErrInvalidToken

// MCPSwitch is the instance-wide MCP flag at runtime. Published from the
// MCP settings row at boot and on every save, so the per-request gate is
// an atomic load rather than a database read. The zero value is off.
type MCPSwitch struct {
	on atomic.Bool
}

func NewMCPSwitch(enabled bool) *MCPSwitch {
	s := &MCPSwitch{}
	s.on.Store(enabled)
	return s
}

// Enabled reports the flag. A nil switch is off.
func (s *MCPSwitch) Enabled() bool {
	return s != nil && s.on.Load()
}

func (s *MCPSwitch) Set(enabled bool) {
	s.on.Store(enabled)
}

// apiTokenStore is the slice of APITokenRepo the service uses.
type apiTokenStore interface {
	Create(ctx context.Context, userID, name string, hash []byte, prefix string, expiresAt *time.Time) (model.APIToken, error)
	ListForUser(ctx context.Context, userID string) ([]model.APIToken, error)
	ListAll(ctx context.Context) ([]repo.APITokenWithOwner, error)
	GetActiveByHash(ctx context.Context, hash []byte, now time.Time) (model.APIToken, error)
	Touch(ctx context.Context, id string, now time.Time) error
	DeleteForUser(ctx context.Context, userID, id string) error
	Delete(ctx context.Context, id string) error
}

// apiTokenUsers resolves a token's owner.
type apiTokenUsers interface {
	GetByID(ctx context.Context, id string) (model.User, error)
}

// APITokenService owns personal access tokens: minting, listing,
// revoking, and resolving a presented token to its user (ADR-0039).
type APITokenService struct {
	tokens apiTokenStore
	users  apiTokenUsers
	mcp    *MCPSwitch
	now    func() time.Time
}

func NewAPITokenService(tokens apiTokenStore, users apiTokenUsers, mcp *MCPSwitch) *APITokenService {
	return &APITokenService{tokens: tokens, users: users, mcp: mcp, now: time.Now}
}

// Create mints a token for userID and returns the plaintext — the only
// time it exists outside the caller. ttl 0 means the token never expires.
func (s *APITokenService) Create(ctx context.Context, userID, name string, ttl time.Duration) (string, model.APIToken, error) {
	if !s.mcp.Enabled() {
		return "", model.APIToken{}, ErrMCPDisabled
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxAPITokenNameLen {
		return "", model.APIToken{}, ErrAPITokenName
	}
	if ttl < 0 {
		return "", model.APIToken{}, errors.New("token lifetime must not be negative")
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", model.APIToken{}, fmt.Errorf("generate token: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(buf)
	raw := APITokenPrefix + body

	var expiresAt *time.Time
	if ttl > 0 {
		t := s.now().Add(ttl)
		expiresAt = &t
	}
	tok, err := s.tokens.Create(ctx, userID, name, HashToken(raw), body[:apiTokenDisplayChars], expiresAt)
	if err != nil {
		return "", model.APIToken{}, err
	}
	return raw, tok, nil
}

// List returns a user's own tokens.
func (s *APITokenService) List(ctx context.Context, userID string) ([]model.APIToken, error) {
	return s.tokens.ListForUser(ctx, userID)
}

// ListAll returns every user's tokens. Admin path.
func (s *APITokenService) ListAll(ctx context.Context) ([]repo.APITokenWithOwner, error) {
	return s.tokens.ListAll(ctx)
}

// Revoke deletes one of userID's own tokens. repo.ErrNotFound when the id
// is malformed, unknown, or someone else's.
func (s *APITokenService) Revoke(ctx context.Context, userID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return repo.ErrNotFound
	}
	return s.tokens.DeleteForUser(ctx, userID, id)
}

// RevokeAny deletes any user's token. Admin path. repo.ErrNotFound when
// the id is malformed or unknown.
func (s *APITokenService) RevokeAny(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return repo.ErrNotFound
	}
	return s.tokens.Delete(ctx, id)
}

// UserByToken resolves a presented token to its owner. Every refusal is
// ErrInvalidAPIToken; anything else is an infrastructure failure.
//
// The owner must be active. Sessions leave the pending gate to the SPA,
// but a token has no SPA in front of it, so a pending or denied user's
// token is refused here.
func (s *APITokenService) UserByToken(ctx context.Context, raw string) (model.User, error) {
	if !strings.HasPrefix(raw, APITokenPrefix) || len(raw) <= len(APITokenPrefix) {
		return model.User{}, ErrInvalidAPIToken
	}
	now := s.now()
	tok, err := s.tokens.GetActiveByHash(ctx, HashToken(raw), now)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return model.User{}, ErrInvalidAPIToken
		}
		return model.User{}, fmt.Errorf("token lookup: %w", err)
	}
	u, err := s.users.GetByID(ctx, tok.UserID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return model.User{}, ErrInvalidAPIToken
		}
		return model.User{}, fmt.Errorf("token owner lookup: %w", err)
	}
	if u.Status != model.UserStatusActive {
		return model.User{}, ErrInvalidAPIToken
	}
	// Bookkeeping, not authentication: a failed touch must not refuse a
	// valid token.
	if err := s.tokens.Touch(ctx, tok.ID, now); err != nil {
		slog.Warn("api token touch", "token", tok.ID, "err", err)
	}
	return u, nil
}
