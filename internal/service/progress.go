package service

import (
	"context"

	"github.com/blackforge/embookshelf/internal/repo"
)

type ProgressService struct {
	repo *repo.ProgressRepo
}

func NewProgressService(r *repo.ProgressRepo) *ProgressService {
	return &ProgressService{repo: r}
}

// Set records a user's progress (0-100, clamped). cfi is optional; pass "" to
// preserve any existing CFI on the row.
func (s *ProgressService) Set(ctx context.Context, userID, bookID string, percent int, cfi string) error {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	return s.repo.Set(ctx, userID, bookID, percent, cfi)
}

// Clear removes the user's progress for a book (mark unread).
func (s *ProgressService) Clear(ctx context.Context, userID, bookID string) error {
	return s.repo.Clear(ctx, userID, bookID)
}
