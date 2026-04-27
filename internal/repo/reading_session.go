package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
)

// ReadingSessionRepo owns the reading_sessions table. RecordTick is
// invoked from ProgressService.Set on every progress update; the read
// queries power the Dashboard heatmap + the /stats reading panel.
type ReadingSessionRepo struct {
	db *db.DB
}

func NewReadingSessionRepo(db *db.DB) *ReadingSessionRepo {
	return &ReadingSessionRepo{db: db}
}

// RecordTick either extends the user's most recent session for a book
// (when the last tick landed inside mergeWindow) or starts a new one.
// Best-effort — failures don't abort progress updates; the caller logs
// the error and moves on.
func (r *ReadingSessionRepo) RecordTick(
	ctx context.Context,
	userID, bookID string,
	percent int,
	mergeWindow time.Duration,
) error {
	// Look up the freshest session for the book and decide whether to
	// extend it. Using a single UPSERT-ish CTE keeps this to one
	// round trip.
	_, err := r.db.SQL.ExecContext(ctx, `
		WITH latest AS (
			SELECT id, ended_at
			FROM reading_sessions
			WHERE user_id = $1 AND book_id = $2
			ORDER BY ended_at DESC
			LIMIT 1
		),
		extended AS (
			UPDATE reading_sessions
			SET ended_at = now(), end_progress = $3
			WHERE id = (
				SELECT id FROM latest
				WHERE ended_at >= now() - $4::interval
			)
			RETURNING 1
		)
		INSERT INTO reading_sessions (user_id, book_id, start_progress, end_progress)
		SELECT $1, $2, $3, $3
		WHERE NOT EXISTS (SELECT 1 FROM extended)
	`, userID, bookID, percent, intervalLiteral(mergeWindow))
	return err
}

// Heatmap returns minute-per-day totals for the last `days` days,
// oldest first (so the client can lay them out left-to-right).
// Sessions shorter than a minute round up to 1 so the UI never loses a
// day to tiny reads.
func (r *ReadingSessionRepo) Heatmap(ctx context.Context, userID string, days int) ([]int, error) {
	if days <= 0 {
		days = 84
	}
	rows, err := r.db.SQL.QueryContext(ctx, `
		WITH day_series AS (
			SELECT generate_series(
				(CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day')::date,
				CURRENT_DATE,
				INTERVAL '1 day'
			)::date AS day
		),
		by_day AS (
			SELECT DATE(started_at) AS day,
			       GREATEST(1,
			           CEIL(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60.0))::int
			       ) AS minutes
			FROM reading_sessions
			WHERE user_id = $1
			  AND started_at >= CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day'
			GROUP BY DATE(started_at)
		)
		SELECT COALESCE(b.minutes, 0)
		FROM day_series d
		LEFT JOIN by_day b ON b.day = d.day
		ORDER BY d.day ASC
	`, userID, days)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]int, 0, days)
	for rows.Next() {
		var m int
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MinutesInWindow sums session durations for the last N days.
// Mirrors the heatmap's "one minute minimum" rounding so short sessions
// aren't invisible in the headline figure either.
func (r *ReadingSessionRepo) MinutesInWindow(ctx context.Context, userID string, days int) (int, error) {
	if days <= 0 {
		days = 7
	}
	var m *int
	err := r.db.SQL.QueryRowContext(ctx, `
		SELECT COALESCE(
			CEIL(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60.0))::int,
			0
		)
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day'
	`, userID, days).Scan(&m)
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, nil
	}
	return *m, nil
}

// CurrentStreak returns the number of consecutive days ending today (or
// yesterday) that have at least one session. A gap of one full day
// breaks the streak.
func (r *ReadingSessionRepo) CurrentStreak(ctx context.Context, userID string) (int, error) {
	// Pull the distinct dates with activity (newest first) and walk
	// them in Go — clearer than a gap-detection window function, and
	// the list is at most ~366 rows for a year.
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT DISTINCT DATE(started_at) AS day
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - INTERVAL '400 days'
		ORDER BY day DESC
	`, userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var days []time.Time
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		days = append(days, d)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(days) == 0 {
		return 0, nil
	}

	// Allow "streak is alive" to include yesterday (some users read
	// once a day, so catching them mid-day today would otherwise
	// always reset the count to 0).
	today := time.Now().UTC().Truncate(24 * time.Hour)
	first := days[0]
	gap := daysBetween(first, today)
	if gap > 1 {
		return 0, nil
	}

	streak := 1
	for i := 1; i < len(days); i++ {
		if daysBetween(days[i], days[i-1]) == 1 {
			streak++
			continue
		}
		break
	}
	return streak, nil
}

// TotalMinutes returns the all-time minute count — used by the Dashboard
// subtitle ("you've been reading for N hours this quarter across K
// sessions"). Quarter-scoping is applied by passing days=90.
func (r *ReadingSessionRepo) TotalMinutes(ctx context.Context, userID string) (int, error) {
	var m *int
	err := r.db.SQL.QueryRowContext(ctx, `
		SELECT CEIL(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60.0))::int
		FROM reading_sessions
		WHERE user_id = $1
	`, userID).Scan(&m)
	if err != nil {
		return 0, err
	}
	if m == nil {
		return 0, nil
	}
	return *m, nil
}

// CountSessions returns the number of reading sessions in the last N
// days.  Quarter = 90.
func (r *ReadingSessionRepo) CountSessions(ctx context.Context, userID string, days int) (int, error) {
	if days <= 0 {
		days = 90
	}
	var n int
	err := r.db.SQL.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day'
	`, userID, days).Scan(&n)
	return n, err
}

// intervalLiteral formats a time.Duration as a Postgres interval literal
// (e.g. "600 seconds"). Go's Duration.String() uses its own syntax
// ("10m0s") which Postgres doesn't accept.
func intervalLiteral(d time.Duration) string {
	secs := int(d.Seconds())
	if secs <= 0 {
		secs = 1
	}
	return fmt.Sprintf("%d seconds", secs)
}

// daysBetween counts calendar days between two UTC-truncated dates.
// Used to decide whether consecutive "days with activity" are actually
// adjacent.
func daysBetween(a, b time.Time) int {
	if a.After(b) {
		a, b = b, a
	}
	hours := b.Sub(a).Hours()
	return int(hours / 24)
}
