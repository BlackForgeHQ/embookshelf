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
