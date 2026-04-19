package service

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/repo"
)

// ReadingStats is the payload the Dashboard + /stats page consume.
// One service call; every field independently fetched in parallel.
type ReadingStats struct {
	HeatmapDays      int
	HeatmapMinutes   []int
	ThisWeekMinutes  int
	CurrentStreak    int
	QuarterMinutes   int
	QuarterSessions  int
	AllTimeMinutes   int
}

type ReadingSessionService struct {
	repo *repo.ReadingSessionRepo
}

func NewReadingSessionService(r *repo.ReadingSessionRepo) *ReadingSessionService {
	return &ReadingSessionService{repo: r}
}

// Collect runs every aggregate in parallel. Mirrors StatsService.Collect
// — the reader stats and the library stats intentionally live in
// separate services so one heavy query doesn't stall the other.
func (s *ReadingSessionService) Collect(ctx context.Context, userID string, heatmapDays int) (ReadingStats, error) {
	if heatmapDays <= 0 {
		heatmapDays = 84
	}
	out := ReadingStats{HeatmapDays: heatmapDays}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		m, err := s.repo.Heatmap(gctx, userID, heatmapDays)
		out.HeatmapMinutes = m
		return err
	})
	g.Go(func() error {
		m, err := s.repo.MinutesInWindow(gctx, userID, 7)
		out.ThisWeekMinutes = m
		return err
	})
	g.Go(func() error {
		n, err := s.repo.CurrentStreak(gctx, userID)
		out.CurrentStreak = n
		return err
	})
	g.Go(func() error {
		m, err := s.repo.MinutesInWindow(gctx, userID, 90)
		out.QuarterMinutes = m
		return err
	})
	g.Go(func() error {
		n, err := s.repo.CountSessions(gctx, userID, 90)
		out.QuarterSessions = n
		return err
	})
	g.Go(func() error {
		m, err := s.repo.TotalMinutes(gctx, userID)
		out.AllTimeMinutes = m
		return err
	})
	if err := g.Wait(); err != nil {
		return ReadingStats{}, err
	}
	return out, nil
}
