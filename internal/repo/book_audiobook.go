// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/db/dberr"
	"github.com/blackforge/embookshelf/internal/model"
)

// BookAudiobookRepo persists generated narrations and their segments.
//
// Deliberately separate from BookRepo for the same reason reading guides
// are (ADR-0024): this is derived state, and a column on books would
// carry it through UpdateMetadata into the sidecar and the reader's own
// EPUB.
type BookAudiobookRepo struct {
	db *db.DB
}

func NewBookAudiobookRepo(d *db.DB) *BookAudiobookRepo {
	return &BookAudiobookRepo{db: d}
}

const audiobookCols = `
	book_id, state, engine, voice, model, source_content_hash,
	file_id, error, total_chars, duration_ms, created_at, updated_at
`

const segmentCols = `
	id, book_id, seq, chapter_index, chapter_title, char_start, char_end,
	start_ms, duration_ms, staged_path, state, error
`

// Start replaces any previous run for a book with a fresh pending one and
// its full segment plan, in a single transaction.
//
// All-or-nothing because a half-written plan is worse than no plan: the
// finalize step trusts that seq 0..n-1 all exist, and a partial insert
// would produce a book missing a chapter with nothing to say so.
// Regeneration is destructive by design (ADR-0025 §4) — the old segments
// go, and the caller has already dealt with the old audio file.
func (r *BookAudiobookRepo) Start(ctx context.Context, ab model.Audiobook, segments []model.AudiobookSegment) error {
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const upsert = `
		INSERT INTO book_audiobooks (
			book_id, state, engine, voice, model, source_content_hash,
			file_id, error, total_chars, duration_ms, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL, '', $7, 0, now(), now())
		ON CONFLICT (book_id) DO UPDATE SET
			state               = EXCLUDED.state,
			engine              = EXCLUDED.engine,
			voice               = EXCLUDED.voice,
			model               = EXCLUDED.model,
			source_content_hash = EXCLUDED.source_content_hash,
			file_id             = NULL,
			error               = '',
			total_chars         = EXCLUDED.total_chars,
			duration_ms         = 0,
			updated_at          = now()
	`
	if _, err := tx.ExecContext(ctx, upsert,
		ab.BookID, string(model.AudiobookPending), ab.Engine, ab.Voice, ab.Model,
		ab.SourceContentHash, ab.TotalChars,
	); err != nil {
		return fmt.Errorf("upsert audiobook: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM book_audiobook_segments WHERE book_id = $1`, ab.BookID); err != nil {
		return fmt.Errorf("clear segments: %w", err)
	}

	const insertSeg = `
		INSERT INTO book_audiobook_segments (
			book_id, seq, chapter_index, chapter_title,
			char_start, char_end, state
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending')
	`
	for _, s := range segments {
		if _, err := tx.ExecContext(ctx, insertSeg,
			ab.BookID, s.Seq, s.ChapterIndex, s.ChapterTitle, s.CharStart, s.CharEnd,
		); err != nil {
			return fmt.Errorf("insert segment %d: %w", s.Seq, err)
		}
	}
	return tx.Commit()
}

func (r *BookAudiobookRepo) GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error) {
	const q = `SELECT ` + audiobookCols + ` FROM book_audiobooks WHERE book_id = $1`

	var (
		ab    model.Audiobook
		state string
	)
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := row.Scan(
		&ab.BookID, &state, &ab.Engine, &ab.Voice, &ab.Model, &ab.SourceContentHash,
		&ab.FileID, &ab.Error, &ab.TotalChars, &ab.DurationMS, &ab.CreatedAt, &ab.UpdatedAt,
	); err != nil {
		if dberr.IsNotFound(err) {
			return model.Audiobook{}, ErrNotFound
		}
		return model.Audiobook{}, err
	}
	ab.State = model.AudiobookState(state)
	return ab, nil
}

// ListSegments returns every segment in playback order.
func (r *BookAudiobookRepo) ListSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error) {
	const q = `SELECT ` + segmentCols + ` FROM book_audiobook_segments WHERE book_id = $1 ORDER BY seq`
	rows, err := r.db.SQL.QueryContext(ctx, q, bookID)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, scanSegment)
}

// GetSegment reads one segment by (book, seq) — the address a job carries.
func (r *BookAudiobookRepo) GetSegment(ctx context.Context, bookID string, seq int) (model.AudiobookSegment, error) {
	const q = `SELECT ` + segmentCols + ` FROM book_audiobook_segments WHERE book_id = $1 AND seq = $2`
	s, err := scanSegment(r.db.SQL.QueryRowContext(ctx, q, bookID, seq))
	if err != nil {
		if dberr.IsNotFound(err) {
			return model.AudiobookSegment{}, ErrNotFound
		}
		return model.AudiobookSegment{}, err
	}
	return s, nil
}

// ListUnfinishedSegments returns the segments a Retry should re-enqueue:
// everything not already done.
//
// Running is included deliberately. A worker killed mid-call leaves its
// row running forever, and excluding those would make Retry silently skip
// exactly the segment that needs it.
func (r *BookAudiobookRepo) ListUnfinishedSegments(ctx context.Context, bookID string) ([]model.AudiobookSegment, error) {
	const q = `SELECT ` + segmentCols + `
		FROM book_audiobook_segments
		WHERE book_id = $1 AND state <> 'done'
		ORDER BY seq`
	rows, err := r.db.SQL.QueryContext(ctx, q, bookID)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, scanSegment)
}

// MarkSegmentRunning claims a segment. Reports whether the claim landed:
// a segment already done must not be re-synthesized, because that is a
// second bill for audio already paid for.
func (r *BookAudiobookRepo) MarkSegmentRunning(ctx context.Context, bookID string, seq int) (bool, error) {
	const q = `
		UPDATE book_audiobook_segments
		SET state = 'running', error = '', updated_at = now()
		WHERE book_id = $1 AND seq = $2 AND state <> 'done'
	`
	res, err := r.db.SQL.ExecContext(ctx, q, bookID, seq)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RecordSegment writes one segment's result, reads the coverage it
// produced, and moves the run — all in one transaction — returning what
// the run needs next.
//
// One operation rather than the write-then-advance pair it replaces. A
// worker that recorded a segment and was killed before advancing left
// every segment done, the run running, and no finalize job: River saw a
// job that had succeeded, Retry refused because nothing was outstanding,
// and the staging sweeper matched only failed and canceled runs, so the
// book sat at 0% and its staging was never reclaimed. Folding the
// coverage read and the state transition into the segment's own write
// closes the window and, more to the point, removes the sequencing a
// caller had to remember: there is no longer a second call to forget.
//
// The run row is locked FOR UPDATE before the segment is written, which
// is what makes the derived transition correct under the concurrent
// segment workers ADR-0028 §3 puts on this queue. Without the lock, two
// segments completing at the same instant each read a snapshot missing
// the other's uncommitted write, both conclude the run is unfinished,
// and neither dispatches finalize — the same strand reached by a
// different route.
func (r *BookAudiobookRepo) RecordSegment(
	ctx context.Context,
	bookID string,
	seq int,
	res model.SegmentResult,
) (model.AudiobookOutcome, error) {
	var zero model.AudiobookOutcome

	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	err = tx.QueryRowContext(ctx,
		`SELECT state FROM book_audiobooks WHERE book_id = $1 FOR UPDATE`, bookID).Scan(&state)
	if err != nil {
		if dberr.IsNotFound(err) {
			return zero, ErrNotFound
		}
		return zero, fmt.Errorf("lock audiobook %s: %w", bookID, err)
	}

	// staged_path and duration_ms are written unconditionally rather than
	// preserved on the failure branch: a segment is claimed only while it
	// is not already done (MarkSegmentRunning), so nothing that has audio
	// can arrive here reporting a failure.
	const writeSeg = `
		UPDATE book_audiobook_segments
		SET state = $3, staged_path = $4, duration_ms = $5, error = $6, updated_at = now()
		WHERE book_id = $1 AND seq = $2
	`
	if _, err := tx.ExecContext(ctx, writeSeg, bookID, seq,
		string(res.State), res.StagedPath, res.DurationMS, res.Error); err != nil {
		return zero, fmt.Errorf("record segment %d: %w", seq, err)
	}

	cov, err := scanCoverage(ctx, tx, bookID)
	if err != nil {
		return zero, fmt.Errorf("coverage for %s: %w", bookID, err)
	}

	next := model.NextForRun(model.AudiobookState(state), cov)
	if next == model.AudiobookNextFail {
		// The staging directory is deliberately untouched. Retry
		// re-enqueues only the segments that never finished, so every
		// paid-for one has to survive the failure that stopped the run
		// (ADR-0028 §6) — failure keeps the work, cancel does not.
		if _, err := tx.ExecContext(ctx,
			`UPDATE book_audiobooks SET state = 'failed', error = $2, updated_at = now() WHERE book_id = $1`,
			bookID, cov.FailureMessage()); err != nil {
			return zero, fmt.Errorf("mark run failed: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return model.AudiobookOutcome{Coverage: cov, Next: next}, nil
}

// SetSegmentStart records where a segment sits in the finished file.
// Written at finalize, when every preceding duration is finally known.
func (r *BookAudiobookRepo) SetSegmentStart(ctx context.Context, bookID string, seq int, startMS int64) error {
	const q = `UPDATE book_audiobook_segments SET start_ms = $3 WHERE book_id = $1 AND seq = $2`
	_, err := r.db.SQL.ExecContext(ctx, q, bookID, seq, startMS)
	return err
}

// Coverage counts segments by state in one query, so progress cannot be
// assembled from two reads taken a moment apart. Same shape as the
// reading-guide run's CountCoverage, for the same reason.
func (r *BookAudiobookRepo) Coverage(ctx context.Context, bookID string) (model.AudiobookCoverage, error) {
	return scanCoverage(ctx, r.db.SQL, bookID)
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx, so the coverage
// query has one text whether it is read on its own or inside the
// transaction that just wrote a segment.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanCoverage(ctx context.Context, q rowQuerier, bookID string) (model.AudiobookCoverage, error) {
	const sel = `
		SELECT count(*) AS total,
		       count(*) FILTER (WHERE state = 'done')   AS done,
		       count(*) FILTER (WHERE state = 'failed') AS failed
		FROM book_audiobook_segments
		WHERE book_id = $1
	`
	var c model.AudiobookCoverage
	if err := q.QueryRowContext(ctx, sel, bookID).Scan(&c.Total, &c.Done, &c.Failed); err != nil {
		return model.AudiobookCoverage{}, err
	}
	return c, nil
}

// SetState moves the run. msg is the failure reason; empty otherwise.
func (r *BookAudiobookRepo) SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error {
	const q = `UPDATE book_audiobooks SET state = $2, error = $3, updated_at = now() WHERE book_id = $1`
	return execOne(ctx, r.db.SQL, q, bookID, string(state), msg)
}

// SetReady completes a run: the file exists, the duration is known.
func (r *BookAudiobookRepo) SetReady(ctx context.Context, bookID, fileID string, durationMS int64) error {
	const q = `
		UPDATE book_audiobooks
		SET state = 'ready', file_id = $2, duration_ms = $3, error = '', updated_at = now()
		WHERE book_id = $1
	`
	_, err := r.db.SQL.ExecContext(ctx, q, bookID, fileID, durationMS)
	return err
}

// Delete removes a run and, by cascade, its segments.
func (r *BookAudiobookRepo) Delete(ctx context.Context, bookID string) error {
	_, err := r.db.SQL.ExecContext(ctx, `DELETE FROM book_audiobooks WHERE book_id = $1`, bookID)
	return err
}

// ListStaleStaging returns runs whose staged segments have become dead
// weight. Feeds the sweeper that keeps abandoned runs from parking
// gigabytes indefinitely.
//
// Keyed on what the segments say rather than on the state column, which
// is the same correction NextForRun makes. The predicate used to be
// `state IN ('failed','canceled')`, so a run stranded at running — every
// segment done, no finalize job — matched nothing and kept its staging
// forever, the third of the three ways #157 made such a run
// unrecoverable.
//
// The one case deliberately withheld is a pending or running run whose
// coverage is complete: that run is a single finalize away from a
// finished book, and reclaiming its staging would convert something
// recoverable into eight dollars of audio that has to be bought again.
// Everything else is fair game once it is older than the TTL — a
// published run's staging is redundant, and a run still missing segments
// has passed the window in which someone was going to retry it.
func (r *BookAudiobookRepo) ListStaleStaging(ctx context.Context, olderThanDays int) ([]string, error) {
	// make_interval rather than `($1 || ' days')::interval`: the
	// concatenation types the placeholder as text, and the driver refuses
	// to encode an int into it, so the previous query returned an error on
	// every call and the sweep had never once run.
	const q = `
		SELECT a.book_id FROM book_audiobooks a
		WHERE a.updated_at < now() - make_interval(days => $1)
		  AND (
		        a.state IN ('ready', 'failed', 'canceled')
		     OR EXISTS (
		          SELECT 1 FROM book_audiobook_segments s
		          WHERE s.book_id = a.book_id AND s.state <> 'done'
		        )
		  )
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, olderThanDays)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (string, error) {
		var id string
		err := s.Scan(&id)
		return id, err
	})
}

func scanSegment(s scanner) (model.AudiobookSegment, error) {
	var (
		seg   model.AudiobookSegment
		state string
		title sql.NullString
	)
	if err := s.Scan(
		&seg.ID, &seg.BookID, &seg.Seq, &seg.ChapterIndex, &title,
		&seg.CharStart, &seg.CharEnd, &seg.StartMS, &seg.DurationMS,
		&seg.StagedPath, &state, &seg.Error,
	); err != nil {
		return model.AudiobookSegment{}, err
	}
	seg.ChapterTitle = title.String
	seg.State = model.SegmentState(state)
	return seg, nil
}
