// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// The sessions table used to keep its column list in two hand-kept
// RETURNING clauses with the Scan destinations stated separately again —
// the Column-order coupling hazard from CONTEXT.md. Three of the six
// columns are TIMESTAMPTZ (expires_at, created_at, last_used_at), so
// swapping two of them compiled, ran, and crossed every session's
// creation time with its expiry: an auth-surface bug that reads as an
// expiry problem.
//
// The defence below is that every field carries a value distinct from
// every other field of its type, and the timestamps sit at clearly
// different instants rather than at now() three times.
//
// One honest limitation: created_at and last_used_at both default to
// now(), so on a freshly inserted row they are equal and a crossing of
// exactly those two in Create's list is invisible. GetActive slides
// last_used_at forward, which is the moment the pair becomes
// distinguishable — so that is where the pair is pinned.

// assertSessionInstant reports a timestamp that landed outside its
// expected window, naming the field so a crossing is readable.
func assertSessionInstant(t *testing.T, where, field string, got, want time.Time, tol time.Duration) {
	t.Helper()
	if delta := got.Sub(want); delta > tol || delta < -tol {
		t.Errorf("%s: %s = %v, want within %v of %v (off by %v) — a timestamp column/scan-order crossing looks exactly like this",
			where, field, got, tol, want, delta)
	}
}

// assertSessionFields checks field by field rather than with one struct
// equality, so a failure names the columns that crossed.
func assertSessionFields(t *testing.T, where string, got model.Session, wantID, wantUserID, wantAgent string) {
	t.Helper()
	if got.ID != wantID {
		t.Errorf("%s: ID = %q, want %q", where, got.ID, wantID)
	}
	if got.UserID != wantUserID {
		t.Errorf("%s: UserID = %q, want %q — the two uuid columns crossed", where, got.UserID, wantUserID)
	}
	if got.ID == got.UserID {
		t.Errorf("%s: ID and UserID are both %q — the uuid columns crossed", where, got.ID)
	}
	if got.UserAgent != wantAgent {
		t.Errorf("%s: UserAgent = %q, want %q", where, got.UserAgent, wantAgent)
	}
	for _, c := range []struct {
		field string
		ts    time.Time
	}{
		{"ExpiresAt", got.ExpiresAt},
		{"CreatedAt", got.CreatedAt},
		{"LastUsedAt", got.LastUsedAt},
	} {
		if c.ts.IsZero() {
			t.Errorf("%s: %s is zero — that timestamp column did not land", where, c.field)
		}
	}
}

// TestSessionRepo_RoundTripPreservesEveryField exercises both of the
// table's RETURNING lists — Create's and GetActive's — with values that
// cannot be confused for one another.
func TestSessionRepo_RoundTripPreservesEveryField(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	users := repo.NewUserRepo(d)
	sessions := repo.NewSessionRepo(d)

	owner, err := users.Create(ctx, "session-owner@example.com", "Session Owner", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	// A user who owns no session, so a crossed user_id has somewhere
	// wrong to point.
	other, err := users.Create(ctx, "not-the-owner@example.com", "Other", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	const (
		userAgent  = "round-trip-agent/1.0 (distinct from every other string)"
		createTTL  = 72 * time.Hour
		extendTTL  = 500 * time.Hour
		instantTol = 30 * time.Second
	)

	before := time.Now()
	created, err := sessions.Create(ctx, owner.ID, userAgent, createTTL)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("Create returned an empty id")
	}
	if created.ID == other.ID {
		t.Fatalf("Create: ID = %q, which is the other user's id", created.ID)
	}
	assertSessionFields(t, "Create", created, created.ID, owner.ID, userAgent)
	// created_at is the insert instant; expires_at is 72h past it. The
	// two are unmistakable, so a crossing of that pair shows up here.
	assertSessionInstant(t, "Create", "CreatedAt", created.CreatedAt, before, instantTol)
	assertSessionInstant(t, "Create", "LastUsedAt", created.LastUsedAt, before, instantTol)
	assertSessionInstant(t, "Create", "ExpiresAt", created.ExpiresAt, before.Add(createTTL), instantTol)

	got, u, err := sessions.GetActive(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	assertSessionFields(t, "GetActive", got, created.ID, owner.ID, userAgent)
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("GetActive: CreatedAt = %v, want %v — the slide rewrote the creation time",
			got.CreatedAt, created.CreatedAt)
	}
	if !got.ExpiresAt.Equal(created.ExpiresAt) {
		t.Errorf("GetActive: ExpiresAt = %v, want %v — the slide moved the expiry",
			got.ExpiresAt, created.ExpiresAt)
	}
	// The only moment created_at and last_used_at can be told apart: the
	// slide moves last_used_at forward and leaves created_at alone.
	if !got.LastUsedAt.After(got.CreatedAt) {
		t.Errorf("GetActive: LastUsedAt (%v) is not after CreatedAt (%v) — the two timestamp columns crossed",
			got.LastUsedAt, got.CreatedAt)
	}
	if !got.LastUsedAt.After(created.LastUsedAt) {
		t.Errorf("GetActive: LastUsedAt (%v) did not slide forward from %v",
			got.LastUsedAt, created.LastUsedAt)
	}
	// GetActive's second query hands back the session's user, so a
	// crossed user_id would surface as the wrong account.
	if u.ID != owner.ID || u.Email != "session-owner@example.com" {
		t.Errorf("GetActive returned user %q/%q, want %q/session-owner@example.com",
			u.ID, u.Email, owner.ID)
	}

	// Extend writes only expires_at. Its statement numbers $2 before $1
	// on purpose so the (id, ttl) argument order works; this proves the
	// pairing is right and that the other timestamps stayed put.
	if err := sessions.Extend(ctx, created.ID, extendTTL); err != nil {
		t.Fatalf("Extend: %v", err)
	}
	extended, _, err := sessions.GetActive(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetActive after Extend: %v", err)
	}
	assertSessionInstant(t, "Extend", "ExpiresAt", extended.ExpiresAt, before.Add(extendTTL), instantTol)
	if !extended.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("Extend: CreatedAt = %v, want %v — the extend rewrote the creation time",
			extended.CreatedAt, created.CreatedAt)
	}
	assertSessionFields(t, "Extend", extended, created.ID, owner.ID, userAgent)
}

// TestSessionRepo_DeleteForUser covers the eviction that makes a password
// reset actually lock an intruder out: every session of the target user goes,
// and no other user's does.
func TestSessionRepo_DeleteForUser(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	sessions := repo.NewSessionRepo(d)
	ctx := context.Background()

	alice, err := users.Create(ctx, "alice@example.com", "Alice", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := users.Create(ctx, "bob@example.com", "Bob", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	var aliceSessions []string
	for _, ua := range []string{"laptop", "phone", "the intruder"} {
		s, err := sessions.Create(ctx, alice.ID, ua, time.Hour)
		if err != nil {
			t.Fatalf("create session %q: %v", ua, err)
		}
		aliceSessions = append(aliceSessions, s.ID)
	}
	bobSession, err := sessions.Create(ctx, bob.ID, "bob's laptop", time.Hour)
	if err != nil {
		t.Fatalf("create bob session: %v", err)
	}

	n, err := sessions.DeleteForUser(ctx, alice.ID)
	if err != nil {
		t.Fatalf("DeleteForUser: %v", err)
	}
	if n != int64(len(aliceSessions)) {
		t.Fatalf("deleted %d, want %d", n, len(aliceSessions))
	}
	for _, id := range aliceSessions {
		if _, _, err := sessions.GetActive(ctx, id); !errors.Is(err, repo.ErrNotFound) {
			t.Errorf("session %s still active after eviction (err=%v)", id, err)
		}
	}
	if _, _, err := sessions.GetActive(ctx, bobSession.ID); err != nil {
		t.Fatalf("bob's session was collateral damage: %v", err)
	}
}

// TestSessionRepo_DeleteForUserNoSessions — eviction runs on every password
// change, including for users who are not signed in anywhere.
func TestSessionRepo_DeleteForUserNoSessions(t *testing.T) {
	d := repotest.New(t)
	users := repo.NewUserRepo(d)
	sessions := repo.NewSessionRepo(d)
	ctx := context.Background()

	u, err := users.Create(ctx, "nobody@example.com", "Nobody", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	n, err := sessions.DeleteForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("DeleteForUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("deleted %d, want 0", n)
	}
}
