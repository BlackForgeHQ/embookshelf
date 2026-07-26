// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// --- fakes -------------------------------------------------------------

type fakeResetUsers struct {
	byEmail    map[string]model.User
	byID       map[string]model.User
	written    map[string]string // userID -> new hash
	updateErr  error
	getByIDErr error
}

func newFakeResetUsers(users ...model.User) *fakeResetUsers {
	f := &fakeResetUsers{
		byEmail: map[string]model.User{},
		byID:    map[string]model.User{},
		written: map[string]string{},
	}
	for _, u := range users {
		f.byEmail[u.Email] = u
		f.byID[u.ID] = u
	}
	return f
}

func (f *fakeResetUsers) GetByEmail(_ context.Context, email string) (model.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return model.User{}, repo.ErrNotFound
	}
	return u, nil
}

func (f *fakeResetUsers) GetByID(_ context.Context, id string) (model.User, error) {
	if f.getByIDErr != nil {
		return model.User{}, f.getByIDErr
	}
	u, ok := f.byID[id]
	if !ok {
		return model.User{}, repo.ErrNotFound
	}
	return u, nil
}

func (f *fakeResetUsers) UpdatePassword(_ context.Context, id, hash string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.written[id] = hash
	return nil
}

type fakeResetTokens struct {
	// hash string -> row; a row is removed from usable on consume.
	usable    map[string]repo.PasswordResetToken
	consumed  []string
	recent    int
	countErr  error
	verifyErr error
}

func (f *fakeResetTokens) CountRecentForUser(context.Context, string, time.Time) (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.recent, nil
}

func (f *fakeResetTokens) Verify(_ context.Context, hash []byte, _ time.Time) (repo.PasswordResetToken, error) {
	if f.verifyErr != nil {
		return repo.PasswordResetToken{}, f.verifyErr
	}
	row, ok := f.usable[string(hash)]
	if !ok {
		return repo.PasswordResetToken{}, repo.ErrNotFound
	}
	return row, nil
}

func (f *fakeResetTokens) Consume(_ context.Context, hash []byte, _ time.Time) (repo.PasswordResetToken, error) {
	row, ok := f.usable[string(hash)]
	if !ok {
		return repo.PasswordResetToken{}, repo.ErrNotFound
	}
	delete(f.usable, string(hash))
	f.consumed = append(f.consumed, string(hash))
	return row, nil
}

type fakeSessionEvictor struct {
	evicted []string
	err     error
}

func (f *fakeSessionEvictor) DeleteForUser(_ context.Context, userID string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.evicted = append(f.evicted, userID)
	return 1, nil
}

type fakeResetIssuer struct {
	issued []model.User
	err    error
}

func (f *fakeResetIssuer) IssuePasswordReset(_ context.Context, u model.User) error {
	if f.err != nil {
		return f.err
	}
	f.issued = append(f.issued, u)
	return nil
}

// --- harness -----------------------------------------------------------

type resetHarness struct {
	svc      *PasswordResetService
	users    *fakeResetUsers
	tokens   *fakeResetTokens
	sessions *fakeSessionEvictor
	issuer   *fakeResetIssuer
}

const (
	testResetToken = "plaintext-token"
	goodPassword   = "correct horse battery staple"
)

func newResetHarness(t *testing.T, users ...model.User) *resetHarness {
	t.Helper()
	if len(users) == 0 {
		users = []model.User{{ID: "u1", Email: "alice@example.com", PasswordHash: "old-hash"}}
	}
	h := &resetHarness{
		users:    newFakeResetUsers(users...),
		tokens:   &fakeResetTokens{usable: map[string]repo.PasswordResetToken{}},
		sessions: &fakeSessionEvictor{},
		issuer:   &fakeResetIssuer{},
	}
	h.svc = NewPasswordResetService(h.users, h.tokens, h.sessions, h.issuer)
	return h
}

// liveToken makes testResetToken resolve to userID.
func (h *resetHarness) liveToken(userID string) {
	h.tokens.usable[string(HashToken(testResetToken))] = repo.PasswordResetToken{
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
}

// --- Request -----------------------------------------------------------

func TestPasswordResetRequestIssuesForKnownUser(t *testing.T) {
	h := newResetHarness(t)
	if err := h.svc.Request(context.Background(), "alice@example.com"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(h.issuer.issued) != 1 || h.issuer.issued[0].ID != "u1" {
		t.Fatalf("issued = %+v, want one for u1", h.issuer.issued)
	}
}

func TestPasswordResetRequestSkipsUnknownEmail(t *testing.T) {
	h := newResetHarness(t)
	if err := h.svc.Request(context.Background(), "nobody@example.com"); err != nil {
		t.Fatalf("Request must not surface a miss: %v", err)
	}
	if len(h.issuer.issued) != 0 {
		t.Fatalf("issued for unknown email: %+v", h.issuer.issued)
	}
}

func TestPasswordResetRequestSkipsBlankEmail(t *testing.T) {
	h := newResetHarness(t)
	for _, in := range []string{"", "   ", "\t"} {
		if err := h.svc.Request(context.Background(), in); err != nil {
			t.Fatalf("Request(%q): %v", in, err)
		}
	}
	if len(h.issuer.issued) != 0 {
		t.Fatalf("issued for blank email: %+v", h.issuer.issued)
	}
}

// TestPasswordResetRequestSkipsPasswordlessUser — an OIDC-only account has no
// local password, so a "reset" would silently become "set an initial
// password", a different flow with different authorisation.
func TestPasswordResetRequestSkipsPasswordlessUser(t *testing.T) {
	h := newResetHarness(t, model.User{ID: "u1", Email: "sso@example.com", PasswordHash: ""})
	if err := h.svc.Request(context.Background(), "sso@example.com"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(h.issuer.issued) != 0 {
		t.Fatalf("issued for passwordless user: %+v", h.issuer.issued)
	}
}

func TestPasswordResetRequestRespectsRateLimit(t *testing.T) {
	h := newResetHarness(t)
	h.tokens.recent = 1
	if err := h.svc.Request(context.Background(), "alice@example.com"); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(h.issuer.issued) != 0 {
		t.Fatalf("issued despite rate limit: %+v", h.issuer.issued)
	}
}

func TestPasswordResetRequestNormalizesEmail(t *testing.T) {
	h := newResetHarness(t)
	if err := h.svc.Request(context.Background(), "  ALICE@Example.COM "); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if len(h.issuer.issued) != 1 {
		t.Fatalf("issued = %+v, want one — email should be normalised before lookup", h.issuer.issued)
	}
}

// TestPasswordResetRequestReportsIssueFailure — delivery failures must reach
// the caller for logging even though the HTTP layer always answers 202.
func TestPasswordResetRequestReportsIssueFailure(t *testing.T) {
	h := newResetHarness(t)
	h.issuer.err = errors.New("smtp down")
	if err := h.svc.Request(context.Background(), "alice@example.com"); err == nil {
		t.Fatal("Request returned nil, want the delivery error for logging")
	}
}

// --- Verify ------------------------------------------------------------

func TestPasswordResetVerifyValidToken(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	got, ok := h.svc.Verify(context.Background(), testResetToken)
	if !ok {
		t.Fatal("Verify said invalid, want valid")
	}
	if got.Email != "alice@example.com" {
		t.Fatalf("Email = %q", got.Email)
	}
	if got.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt zero")
	}
}

func TestPasswordResetVerifyRejects(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*resetHarness)
		token string
	}{
		{"blank token", func(*resetHarness) {}, "  "},
		{"unknown token", func(*resetHarness) {}, testResetToken},
		{"user vanished", func(h *resetHarness) {
			h.liveToken("ghost")
		}, testResetToken},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newResetHarness(t)
			tc.setup(h)
			if _, ok := h.svc.Verify(context.Background(), tc.token); ok {
				t.Fatal("Verify said valid, want invalid")
			}
		})
	}
}

// TestPasswordResetVerifyDoesNotConsume — the UI pre-flights every render of
// the reset page; if that spent the token the form would be dead on arrival.
func TestPasswordResetVerifyDoesNotConsume(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	for i := range 3 {
		if _, ok := h.svc.Verify(context.Background(), testResetToken); !ok {
			t.Fatalf("Verify #%d said invalid — token was consumed by a read", i+1)
		}
	}
	if len(h.tokens.consumed) != 0 {
		t.Fatalf("consumed = %v, want none", h.tokens.consumed)
	}
}

// --- Confirm -----------------------------------------------------------

func TestPasswordResetConfirmWritesPassword(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	written, ok := h.users.written["u1"]
	if !ok {
		t.Fatal("no password written")
	}
	if err := auth.VerifyPassword(written, goodPassword); err != nil {
		t.Fatalf("written hash does not verify against the new password: %v", err)
	}
}

// TestPasswordResetConfirmEvictsSessions — a reset is the remedy for a
// compromised account, so any session an intruder already holds must die
// with the old password.
func TestPasswordResetConfirmEvictsSessions(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if len(h.sessions.evicted) != 1 || h.sessions.evicted[0] != "u1" {
		t.Fatalf("evicted = %v, want [u1]", h.sessions.evicted)
	}
}

func TestPasswordResetConfirmConsumesToken(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	err := h.svc.Confirm(context.Background(), testResetToken, "another good password here")
	if !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("replay err = %v, want ErrResetTokenInvalid", err)
	}
}

func TestPasswordResetConfirmRejectsUnknownToken(t *testing.T) {
	h := newResetHarness(t)
	err := h.svc.Confirm(context.Background(), testResetToken, goodPassword)
	if !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("err = %v, want ErrResetTokenInvalid", err)
	}
}

func TestPasswordResetConfirmRejectsBlankInput(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	if err := h.svc.Confirm(context.Background(), "  ", goodPassword); !errors.Is(err, ErrResetTokenInvalid) {
		t.Fatalf("blank token err = %v, want ErrResetTokenInvalid", err)
	}
	if err := h.svc.Confirm(context.Background(), testResetToken, ""); err == nil {
		t.Fatal("blank password accepted")
	}
	if len(h.tokens.consumed) != 0 {
		t.Fatalf("blank input consumed the token: %v", h.tokens.consumed)
	}
}

// TestPasswordResetConfirmKeepsTokenAfterWeakPassword is the regression that
// motivated moving this out of the handler: the old code consumed the token
// before checking password strength, so a user who typed something too short
// got an error AND a dead reset link, then hit the 5-minute rate limit when
// asking for a new one.
func TestPasswordResetConfirmKeepsTokenAfterWeakPassword(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")

	err := h.svc.Confirm(context.Background(), testResetToken, "x")
	if !errors.Is(err, auth.ErrWeakPassword) {
		t.Fatalf("err = %v, want ErrWeakPassword", err)
	}
	if len(h.tokens.consumed) != 0 {
		t.Fatalf("weak password consumed the token: %v", h.tokens.consumed)
	}
	if len(h.users.written) != 0 {
		t.Fatalf("weak password was written: %v", h.users.written)
	}

	// The link must still work on the retry.
	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err != nil {
		t.Fatalf("retry after weak password: %v", err)
	}
	if _, ok := h.users.written["u1"]; !ok {
		t.Fatal("retry did not write the password")
	}
}

// TestPasswordResetConfirmKeepsSessionsWhenWriteFails — evicting on a failed
// write would log the user out of a password they still hold.
func TestPasswordResetConfirmKeepsSessionsWhenWriteFails(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	h.users.updateErr = errors.New("db down")

	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err == nil {
		t.Fatal("Confirm returned nil despite a write failure")
	}
	if len(h.sessions.evicted) != 0 {
		t.Fatalf("evicted despite failed write: %v", h.sessions.evicted)
	}
}

// TestPasswordResetConfirmSucceedsWhenEvictionFails — the new password is
// already live at that point; failing the request would tell the user their
// reset did not work when it did.
func TestPasswordResetConfirmSucceedsWhenEvictionFails(t *testing.T) {
	h := newResetHarness(t)
	h.liveToken("u1")
	h.sessions.err = errors.New("db down")

	if err := h.svc.Confirm(context.Background(), testResetToken, goodPassword); err != nil {
		t.Fatalf("Confirm = %v, want nil — the password write succeeded", err)
	}
}
