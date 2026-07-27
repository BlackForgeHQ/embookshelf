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
	defer func() { _ = rows.Close() }()

	var out []model.AudiobookSegment
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
	defer func() { _ = rows.Close() }()

	var out []model.AudiobookSegment
	for rows.Next() {
		s, err := scanSegment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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

// MarkSegmentDone records a finished segment and where its audio landed.
func (r *BookAudiobookRepo) MarkSegmentDone(ctx context.Context, bookID string, seq int, stagedPath string, durationMS int64) error {
	const q = `
		UPDATE book_audiobook_segments
		SET state = 'done', staged_path = $3, duration_ms = $4, error = '', updated_at = now()
		WHERE book_id = $1 AND seq = $2
	`
	_, err := r.db.SQL.ExecContext(ctx, q, bookID, seq, stagedPath, durationMS)
	return err
}

func (r *BookAudiobookRepo) MarkSegmentFailed(ctx context.Context, bookID string, seq int, msg string) error {
	const q = `
		UPDATE book_audiobook_segments
		SET state = 'failed', error = $3, updated_at = now()
		WHERE book_id = $1 AND seq = $2
	`
	_, err := r.db.SQL.ExecContext(ctx, q, bookID, seq, msg)
	return err
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
	const q = `
		SELECT count(*) AS total,
		       count(*) FILTER (WHERE state = 'done')   AS done,
		       count(*) FILTER (WHERE state = 'failed') AS failed
		FROM book_audiobook_segments
		WHERE book_id = $1
	`
	var c model.AudiobookCoverage
	if err := r.db.SQL.QueryRowContext(ctx, q, bookID).Scan(&c.Total, &c.Done, &c.Failed); err != nil {
		return model.AudiobookCoverage{}, err
	}
	return c, nil
}

// SetState moves the run. msg is the failure reason; empty otherwise.
func (r *BookAudiobookRepo) SetState(ctx context.Context, bookID string, state model.AudiobookState, msg string) error {
	const q = `UPDATE book_audiobooks SET state = $2, error = $3, updated_at = now() WHERE book_id = $1`
	res, err := r.db.SQL.ExecContext(ctx, q, bookID, string(state), msg)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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

// ListStaleTerminal returns runs left in a terminal-but-not-done state
// long enough that their staging is dead weight. Feeds the sweeper that
// keeps abandoned retries from parking gigabytes indefinitely.
func (r *BookAudiobookRepo) ListStaleTerminal(ctx context.Context, olderThanDays int) ([]string, error) {
	const q = `
		SELECT book_id FROM book_audiobooks
		WHERE state IN ('failed', 'canceled')
		  AND updated_at < now() - ($1 || ' days')::interval
	`
	rows, err := r.db.SQL.QueryContext(ctx, q, olderThanDays)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type segmentScanner interface {
	Scan(dest ...any) error
}

func scanSegment(s segmentScanner) (model.AudiobookSegment, error) {
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
