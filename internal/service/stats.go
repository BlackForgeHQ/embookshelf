// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/repo"
)

// Stats is the full aggregate payload the /stats dashboard consumes.
// A single service call populates every field so the frontend only
// needs one request for the whole page.
type Stats struct {
	Totals        StatsTotals
	User          StatsUser
	Libraries     []repo.StatsBucket
	Formats       []repo.StatsBucket
	TopAuthors    []repo.StatsBucket
	TopTags       []repo.StatsBucket
	YearHistogram []repo.StatsYearBucket
	Ratings       []repo.StatsRatingBucket
}

type StatsTotals struct {
	Books          int
	BooksWithCover int
}

type StatsUser struct {
	Reading      int
	Finished     int
	Annotations  int
	Shelves      int
	SmartShelves int
}

type StatsService struct {
	repo *repo.StatsRepo
}

func NewStatsService(r *repo.StatsRepo) *StatsService {
	return &StatsService{repo: r}
}

// Collect runs every aggregate in parallel. Each query is independent,
// so we errgroup them rather than chain — worst case latency ≈ the
// slowest single query instead of the sum.
func (s *StatsService) Collect(ctx context.Context, userID string) (Stats, error) {
	var out Stats
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		n, err := s.repo.CountBooks(gctx)
		out.Totals.Books = n
		return err
	})
	g.Go(func() error {
		n, err := s.repo.CountBooksWithCover(gctx)
		out.Totals.BooksWithCover = n
		return err
	})
	g.Go(func() error {
		libs, err := s.repo.BooksPerLibrary(gctx)
		out.Libraries = libs
		return err
	})
	g.Go(func() error {
		fmts, err := s.repo.BooksPerFormat(gctx)
		out.Formats = fmts
		return err
	})
	g.Go(func() error {
		authors, err := s.repo.TopAuthors(gctx, 10)
		out.TopAuthors = authors
		return err
	})
	g.Go(func() error {
		tags, err := s.repo.TopTags(gctx, 15)
		out.TopTags = tags
		return err
	})
	g.Go(func() error {
		years, err := s.repo.YearHistogram(gctx)
		out.YearHistogram = years
		return err
	})
	g.Go(func() error {
		ratings, err := s.repo.RatingDistribution(gctx)
		out.Ratings = ratings
		return err
	})
	g.Go(func() error {
		reading, finished, err := s.repo.UserProgressCounts(gctx, userID)
		out.User.Reading = reading
		out.User.Finished = finished
		return err
	})
	g.Go(func() error {
		n, err := s.repo.UserAnnotationCount(gctx, userID)
		out.User.Annotations = n
		return err
	})
	g.Go(func() error {
		total, smart, err := s.repo.UserShelfCounts(gctx, userID)
		out.User.Shelves = total
		out.User.SmartShelves = smart
		return err
	})

	if err := g.Wait(); err != nil {
		return Stats{}, err
	}
	return out, nil
}
