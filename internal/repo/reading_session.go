package repo

import (
	"context"
	"database/sql"
	"errors"
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
	// PG path: single CTE round trip with interval arithmetic.
	const qPG = `
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
	`
	if r.db.Dialect == db.DialectSQLite {
		return r.recordTickSQLite(ctx, userID, bookID, percent, mergeWindow)
	}
	_, err := r.db.SQL.ExecContext(ctx, qPG, userID, bookID, percent, intervalLiteral(mergeWindow))
	return err
}

// recordTickSQLite is the SQLite equivalent of the PG single-statement CTE
// chain. SQLite rejects DML inside a CTE (Postgres-only extension), so the
// extend-or-insert decision happens app-side inside a transaction:
//
//  1. SELECT the latest session for (user, book).
//  2. If its ended_at is within mergeWindow → UPDATE that row.
//  3. Otherwise → INSERT a new row with a pre-generated UUID.
//
// Wrapped in a tx so a concurrent tick from another goroutine can't drop
// us into a torn state. RecordTick is already documented as best-effort,
// so any error rolls back and surfaces to the caller.
func (r *ReadingSessionRepo) recordTickSQLite(
	ctx context.Context,
	userID, bookID string,
	percent int,
	mergeWindow time.Duration,
) error {
	secs := int(mergeWindow.Seconds())
	if secs <= 0 {
		secs = 1
	}
	modifier := fmt.Sprintf("-%d seconds", secs)

	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		latestID string
		// String scan because SQLite stores timestamps as TEXT.
		latestEndedAt string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, ended_at
		FROM reading_sessions
		WHERE user_id = ? AND book_id = ?
		ORDER BY ended_at DESC
		LIMIT 1
	`, userID, bookID).Scan(&latestID, &latestEndedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("select latest: %w", err)
	}

	if latestID != "" {
		// Did the latest tick land inside mergeWindow? Push the cutoff
		// computation to SQLite so its clock is the authority — no
		// app/DB drift.
		var withinWindow int
		err = tx.QueryRowContext(ctx, `
			SELECT CASE
				WHEN ? >= datetime('now', ?) THEN 1 ELSE 0
			END
		`, latestEndedAt, modifier).Scan(&withinWindow)
		if err != nil {
			return fmt.Errorf("window check: %w", err)
		}
		if withinWindow == 1 {
			_, err = tx.ExecContext(ctx, `
				UPDATE reading_sessions
				SET ended_at = (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
				    end_progress = ?
				WHERE id = ?
			`, percent, latestID)
			if err != nil {
				return fmt.Errorf("extend session: %w", err)
			}
			return tx.Commit()
		}
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO reading_sessions (id, user_id, book_id, start_progress, end_progress)
		VALUES (?, ?, ?, ?, ?)
	`, db.NewID(), userID, bookID, percent, percent)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return tx.Commit()
}

// Heatmap returns minute-per-day totals for the last `days` days,
// oldest first (so the client can lay them out left-to-right).
// Sessions shorter than a minute round up to 1 so the UI never loses a
// day to tiny reads.
func (r *ReadingSessionRepo) Heatmap(ctx context.Context, userID string, days int) ([]int, error) {
	if days <= 0 {
		days = 84
	}

	if r.db.Dialect == db.DialectSQLite {
		return r.heatmapSQLite(ctx, userID, days)
	}
	return r.heatmapPG(ctx, userID, days)
}

func (r *ReadingSessionRepo) heatmapPG(ctx context.Context, userID string, days int) ([]int, error) {
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

func (r *ReadingSessionRepo) heatmapSQLite(ctx context.Context, userID string, days int) ([]int, error) {
	// Query minutes grouped by date for the window.
	rows, err := r.db.SQL.QueryContext(ctx, `
		SELECT date(started_at) AS day,
		       MAX(1,
		           CAST(CEIL((julianday(ended_at) - julianday(started_at)) * 86400 / 60.0) AS INTEGER)
		       ) AS minutes
		FROM reading_sessions
		WHERE user_id = ?
		  AND started_at >= date('now', ? || ' days')
		GROUP BY date(started_at)
		ORDER BY day ASC
	`, userID, fmt.Sprintf("-%d", days-1))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	// Build a day→minutes map.
	byDay := make(map[string]int, days)
	for rows.Next() {
		var dayStr string
		var m int
		if err := rows.Scan(&dayStr, &m); err != nil {
			return nil, err
		}
		byDay[dayStr] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fill the full window oldest→newest with 0 for missing days.
	now := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]int, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days - 1 - i))
		key := d.Format("2006-01-02")
		out[i] = byDay[key]
	}
	return out, nil
}

// MinutesInWindow sums session durations for the last N days.
// Mirrors the heatmap's "one minute minimum" rounding so short sessions
// aren't invisible in the headline figure either.
func (r *ReadingSessionRepo) MinutesInWindow(ctx context.Context, userID string, days int) (int, error) {
	if days <= 0 {
		days = 7
	}
	const qPG = `
		SELECT COALESCE(
			CEIL(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60.0))::int,
			0
		)
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day'
	`
	const qSQLite = `
		SELECT COALESCE(
			CAST(CEIL(SUM((julianday(ended_at) - julianday(started_at)) * 86400 / 60.0)) AS INTEGER),
			0
		)
		FROM reading_sessions
		WHERE user_id = ?
		  AND started_at >= date('now', ? || ' days')
	`
	var m *int
	var err error
	if r.db.Dialect == db.DialectSQLite {
		err = r.db.SQL.QueryRowContext(ctx, qSQLite,
			userID, fmt.Sprintf("-%d", days-1)).Scan(&m)
	} else {
		err = r.db.SQL.QueryRowContext(ctx, qPG, userID, days).Scan(&m)
	}
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
	const qPG = `
		SELECT DISTINCT DATE(started_at) AS day
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - INTERVAL '400 days'
		ORDER BY day DESC
	`
	const qSQLite = `
		SELECT DISTINCT date(started_at) AS day
		FROM reading_sessions
		WHERE user_id = ?
		  AND started_at >= date('now', '-400 days')
		ORDER BY day DESC
	`
	rows, err := r.db.SQL.QueryContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite), userID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	var days []time.Time
	for rows.Next() {
		var dayAny any
		if err := rows.Scan(&dayAny); err != nil {
			return 0, err
		}
		// PG returns time.Time for DATE columns; SQLite returns a string "YYYY-MM-DD".
		var d time.Time
		switch v := dayAny.(type) {
		case time.Time:
			d = v.UTC()
		case string:
			t, err := time.Parse("2006-01-02", v)
			if err != nil {
				return 0, fmt.Errorf("parse day %q: %w", v, err)
			}
			d = t.UTC()
		default:
			return 0, fmt.Errorf("unexpected day type %T", dayAny)
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
	const qPG = `
		SELECT CEIL(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)) / 60.0))::int
		FROM reading_sessions
		WHERE user_id = $1
	`
	const qSQLite = `
		SELECT CAST(CEIL(SUM((julianday(ended_at) - julianday(started_at)) * 86400 / 60.0)) AS INTEGER)
		FROM reading_sessions
		WHERE user_id = ?
	`
	var m *int
	err := r.db.SQL.QueryRowContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite), userID).Scan(&m)
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
	const qPG = `
		SELECT COUNT(*)
		FROM reading_sessions
		WHERE user_id = $1
		  AND started_at >= CURRENT_DATE - ($2::int - 1) * INTERVAL '1 day'
	`
	const qSQLite = `
		SELECT COUNT(*)
		FROM reading_sessions
		WHERE user_id = ?
		  AND started_at >= date('now', ? || ' days')
	`
	var n int
	var err error
	if r.db.Dialect == db.DialectSQLite {
		err = r.db.SQL.QueryRowContext(ctx, qSQLite,
			userID, fmt.Sprintf("-%d", days-1)).Scan(&n)
	} else {
		err = r.db.SQL.QueryRowContext(ctx, qPG, userID, days).Scan(&n)
	}
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
