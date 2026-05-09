// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/repo"
)

// SessionMergeWindow is the gap between progress ticks that counts as
// "still the same reading session". Picked empirically: long enough
// that a quick bathroom break doesn't split a 90-minute read in two,
// short enough that "I opened this book before dinner, came back after"
// registers as two.
const SessionMergeWindow = 10 * time.Minute

type ProgressService struct {
	repo     *repo.ProgressRepo
	sessions *repo.ReadingSessionRepo
}

// NewProgressService takes the sessions repo so every progress update
// also lands a session tick. Passing nil is acceptable — session
// recording becomes a no-op. Useful in tests + edge-case bootstrap.
func NewProgressService(r *repo.ProgressRepo, s *repo.ReadingSessionRepo) *ProgressService {
	return &ProgressService{repo: r, sessions: s}
}

// Set records a user's progress (0-100, clamped). cfi is optional; pass "" to
// preserve any existing CFI on the row. Best-effort session tick runs
// after the progress write — the reader UX is the priority, so a broken
// session insert logs a warning and returns success.
func (s *ProgressService) Set(ctx context.Context, userID, bookID string, percent int, cfi string) error {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	if err := s.repo.Set(ctx, userID, bookID, percent, cfi); err != nil {
		return err
	}
	if s.sessions != nil {
		if err := s.sessions.RecordTick(ctx, userID, bookID, percent, SessionMergeWindow); err != nil {
			slog.Warn("reading session record tick", "err", err, "userID", userID, "bookID", bookID)
		}
	}
	return nil
}
