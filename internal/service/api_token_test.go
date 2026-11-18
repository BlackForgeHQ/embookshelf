// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// fakeAPITokens is a slice-backed apiTokenStore that applies the same
// expiry rule as the repo, so the service's resolve path can be driven
// without Postgres.
type fakeAPITokens struct {
	rows     []fakeAPITokenRow
	touched  []string
	touchErr error
	lookErr  error
}

type fakeAPITokenRow struct {
	tok  model.APIToken
	hash []byte
}

func (f *fakeAPITokens) Create(_ context.Context, userID, name string, hash []byte, prefix string, expiresAt *time.Time) (model.APIToken, error) {
	t := model.APIToken{
		ID:        fmt.Sprintf("00000000-0000-0000-0000-%012d", len(f.rows)+1),
		UserID:    userID,
		Name:      name,
		Prefix:    prefix,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	f.rows = append(f.rows, fakeAPITokenRow{tok: t, hash: hash})
	return t, nil
}

func (f *fakeAPITokens) ListForUser(_ context.Context, userID string) ([]model.APIToken, error) {
	var out []model.APIToken
	for _, r := range f.rows {
		if r.tok.UserID == userID {
			out = append(out, r.tok)
		}
	}
	return out, nil
}

func (f *fakeAPITokens) ListAll(context.Context) ([]repo.APITokenWithOwner, error) {
	return nil, nil
}

func (f *fakeAPITokens) GetActiveByHash(_ context.Context, hash []byte, now time.Time) (model.APIToken, error) {
	if f.lookErr != nil {
		return model.APIToken{}, f.lookErr
	}
	for _, r := range f.rows {
		if bytes.Equal(r.hash, hash) && (r.tok.ExpiresAt == nil || r.tok.ExpiresAt.After(now)) {
			return r.tok, nil
		}
	}
	return model.APIToken{}, repo.ErrNotFound
}

func (f *fakeAPITokens) Touch(_ context.Context, id string, _ time.Time) error {
	f.touched = append(f.touched, id)
	return f.touchErr
}

func (f *fakeAPITokens) DeleteForUser(_ context.Context, userID, id string) error {
	for i, r := range f.rows {
		if r.tok.ID == id && r.tok.UserID == userID {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return repo.ErrNotFound
}

func (f *fakeAPITokens) Delete(_ context.Context, id string) error {
	for i, r := range f.rows {
		if r.tok.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
			return nil
		}
	}
	return repo.ErrNotFound
}

type fakeTokenUsers map[string]model.User

func (f fakeTokenUsers) GetByID(_ context.Context, id string) (model.User, error) {
	u, ok := f[id]
	if !ok {
		return model.User{}, repo.ErrNotFound
	}
	return u, nil
}

func newTokenService(t *testing.T, enabled bool) (*APITokenService, *fakeAPITokens, fakeTokenUsers) {
	t.Helper()
	store := &fakeAPITokens{}
	users := fakeTokenUsers{
		"u1": {ID: "u1", Email: "reader@example.com", Status: model.UserStatusActive},
	}
	return NewAPITokenService(store, users, NewMCPSwitch(enabled)), store, users
}

func TestAPITokenCreateRefusesWhileMCPIsOff(t *testing.T) {
	svc, store, _ := newTokenService(t, false)

	if _, _, err := svc.Create(context.Background(), "u1", "laptop", 0); !errors.Is(err, ErrMCPDisabled) {
		t.Fatalf("Create with MCP off: err = %v, want ErrMCPDisabled", err)
	}
	if len(store.rows) != 0 {
		t.Fatalf("a refused create stored %d rows", len(store.rows))
	}
}

func TestAPITokenCreateFollowsTheSwitchAtRuntime(t *testing.T) {
	svc, _, _ := newTokenService(t, false)
	svc.mcp.Set(true)

	if _, _, err := svc.Create(context.Background(), "u1", "laptop", 0); err != nil {
		t.Fatalf("Create after the switch flipped on: %v", err)
	}
}

func TestAPITokenCreateValidatesName(t *testing.T) {
	svc, _, _ := newTokenService(t, true)
	for _, name := range []string{"", "   ", strings.Repeat("x", MaxAPITokenNameLen+1)} {
		if _, _, err := svc.Create(context.Background(), "u1", name, 0); !errors.Is(err, ErrAPITokenName) {
			t.Errorf("Create(name=%q): err = %v, want ErrAPITokenName", name, err)
		}
	}
}

func TestAPITokenCreateStoresOnlyTheHash(t *testing.T) {
	svc, store, _ := newTokenService(t, true)

	raw, tok, err := svc.Create(context.Background(), "u1", "  laptop  ", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(raw, APITokenPrefix) {
		t.Fatalf("token %q lacks the %q prefix", raw, APITokenPrefix)
	}
	// 32 random bytes, unpadded base64url.
	if got := len(raw) - len(APITokenPrefix); got != 43 {
		t.Fatalf("token body is %d chars, want 43", got)
	}
	if tok.Name != "laptop" {
		t.Errorf("name = %q, want trimmed %q", tok.Name, "laptop")
	}
	if !strings.HasPrefix(raw, APITokenPrefix+tok.Prefix) || len(tok.Prefix) != apiTokenDisplayChars {
		t.Errorf("display prefix %q is not the first %d chars of the token body", tok.Prefix, apiTokenDisplayChars)
	}
	if tok.ExpiresAt != nil {
		t.Errorf("ttl 0 stored expiry %v, want never", tok.ExpiresAt)
	}
	stored := store.rows[0].hash
	if bytes.Contains(stored, []byte(raw)) || !bytes.Equal(stored, HashToken(raw)) {
		t.Fatal("stored hash is not sha256(token)")
	}
}

func TestAPITokenCreateSetsExpiry(t *testing.T) {
	svc, _, _ := newTokenService(t, true)
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	_, tok, err := svc.Create(context.Background(), "u1", "ci", 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("expiresAt = %v, want %v", tok.ExpiresAt, now.Add(30*24*time.Hour))
	}
}

func TestUserByTokenResolvesAndTouches(t *testing.T) {
	svc, store, _ := newTokenService(t, true)
	raw, tok, _ := svc.Create(context.Background(), "u1", "laptop", 0)

	u, err := svc.UserByToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("UserByToken: %v", err)
	}
	if u.ID != "u1" {
		t.Fatalf("resolved user %q, want u1", u.ID)
	}
	if len(store.touched) != 1 || store.touched[0] != tok.ID {
		t.Fatalf("touched %v, want [%s]", store.touched, tok.ID)
	}
}

func TestUserByTokenIgnoresATouchFailure(t *testing.T) {
	svc, store, _ := newTokenService(t, true)
	raw, _, _ := svc.Create(context.Background(), "u1", "laptop", 0)
	store.touchErr = errors.New("disk full")

	if _, err := svc.UserByToken(context.Background(), raw); err != nil {
		t.Fatalf("a failed touch refused a valid token: %v", err)
	}
}

func TestUserByTokenRefusals(t *testing.T) {
	svc, store, users := newTokenService(t, true)
	now := time.Now()
	svc.now = func() time.Time { return now }

	valid, _, _ := svc.Create(context.Background(), "u1", "laptop", 0)
	expired, _, _ := svc.Create(context.Background(), "u1", "old", time.Hour)
	svc.now = func() time.Time { return now.Add(2 * time.Hour) }

	users["u-pending"] = model.User{ID: "u-pending", Status: model.UserStatusPending}
	users["u-denied"] = model.User{ID: "u-denied", Status: model.UserStatusDenied}
	pending, _, _ := svc.Create(context.Background(), "u-pending", "p", 0)
	denied, _, _ := svc.Create(context.Background(), "u-denied", "d", 0)
	orphan, _, _ := svc.Create(context.Background(), "u-gone", "o", 0)

	cases := map[string]string{
		"empty":           "",
		"no prefix":       strings.TrimPrefix(valid, APITokenPrefix),
		"prefix only":     APITokenPrefix,
		"unknown":         valid + "x",
		"expired":         expired,
		"pending owner":   pending,
		"denied owner":    denied,
		"owner not found": orphan,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.UserByToken(context.Background(), raw); !errors.Is(err, ErrInvalidAPIToken) {
				t.Fatalf("err = %v, want ErrInvalidAPIToken", err)
			}
		})
	}
	if len(store.touched) != 0 {
		t.Errorf("a refused token was touched: %v", store.touched)
	}
}

func TestUserByTokenReportsAnOutageAsAnOutage(t *testing.T) {
	svc, store, _ := newTokenService(t, true)
	raw, _, _ := svc.Create(context.Background(), "u1", "laptop", 0)
	store.lookErr = errors.New("connection refused")

	_, err := svc.UserByToken(context.Background(), raw)
	if err == nil || errors.Is(err, ErrInvalidAPIToken) {
		t.Fatalf("err = %v, want an infrastructure error distinct from ErrInvalidAPIToken", err)
	}
}

func TestRevokeScopesToTheOwner(t *testing.T) {
	svc, store, _ := newTokenService(t, true)
	_, tok, _ := svc.Create(context.Background(), "u1", "laptop", 0)

	if err := svc.Revoke(context.Background(), "someone-else", tok.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("revoking another user's token: err = %v, want ErrNotFound", err)
	}
	if err := svc.Revoke(context.Background(), "u1", "not-a-uuid"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("malformed id: err = %v, want ErrNotFound", err)
	}
	if err := svc.Revoke(context.Background(), "u1", tok.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if len(store.rows) != 0 {
		t.Fatal("token survived its owner's revoke")
	}
}

func TestRevokeAnyRejectsAMalformedID(t *testing.T) {
	svc, _, _ := newTokenService(t, true)
	if err := svc.RevokeAny(context.Background(), "../etc"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMCPSwitchNilIsOff(t *testing.T) {
	var s *MCPSwitch
	if s.Enabled() {
		t.Fatal("a nil switch reads as on")
	}
}
