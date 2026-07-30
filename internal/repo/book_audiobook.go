// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"errors"
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

// generation sits between two TEXT columns on purpose. The list below and
// the Scan destinations in GetByBookID are hand-kept — this table has no
// projection — so the hazard is a *same-type adjacent* swap, which
// compiles and silently crosses two fields (CONTEXT.md §Column-order
// coupling). Wedged between state and engine, a swap in one list and not
// the other is a scan type error rather than a run reporting somebody
// else's generation.
const audiobookCols = `
	book_id, state, generation, engine, voice, model, segment_chars, source_content_hash,
	file_id, error, total_chars, duration_ms, created_at, updated_at
`

const segmentCols = `
	id, book_id, seq, chapter_index, chapter_title, char_start, char_end,
	start_ms, duration_ms, staged_path, state, error
`

// ErrRunInProgress refuses a start over a run that has not concluded.
//
// The first already-running refusal on this path: Generate → Preflight →
// Start had none, and the upsert below went to pending unconditionally.
// What that wiped was not a stale record but a live plan whose completed
// segments are audio already bought, while the jobs still working through
// it carried on spending. Cancel is the stop-loss ADR-0028 §6 puts in
// front of a run that can cost $170, and this makes a user take it
// deliberately rather than lose the same money by pressing Generate
// twice.
//
// The handler answers it 409 through writeAudiobookError's default arm,
// beside cancel-a-finished-run and retry-with-nothing-outstanding: one
// category, "your view of this run is stale", and no code the client has
// to learn.
var ErrRunInProgress = errors.New(
	"a narration is already being generated for this book — cancel it before starting another")

// Start replaces a concluded run for a book with a fresh pending one and
// its full segment plan, in a single transaction, and reports the
// generation it assigned.
//
// All-or-nothing because a half-written plan is worse than no plan: the
// finalize step trusts that seq 0..n-1 all exist, and a partial insert
// would produce a book missing a chapter with nothing to say so.
// Regeneration is destructive by design (ADR-0025 §4) — the old segments
// go, and the caller has already dealt with the old audio file.
//
// Destructive over a *concluded* run only. Over a pending or running one
// it refuses with ErrRunInProgress: see that sentinel.
//
// The generation it returns is the run's identity, and the caller carries
// it into every segment job it dispatches. Bumping it here is what makes
// the jobs of the plan this call just deleted address a run that no
// longer exists (ADR-0031).
func (r *BookAudiobookRepo) Start(
	ctx context.Context, ab model.Audiobook, segments []model.AudiobookSegment,
) (int, error) {
	tx, err := r.db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// A first run starts at generation 1, not 0, so that 0 keeps the one
	// meaning the deploy story needs: a row nothing has started since the
	// column was added. See the migration.
	const upsert = `
		INSERT INTO book_audiobooks (
			book_id, state, generation, engine, voice, model, segment_chars, source_content_hash,
			file_id, error, total_chars, duration_ms, created_at, updated_at
		) VALUES ($1, $2, 1, $3, $4, $5, $6, $7, NULL, '', $8, 0, now(), now())
		ON CONFLICT (book_id) DO UPDATE SET
			state               = EXCLUDED.state,
			generation          = book_audiobooks.generation + 1,
			engine              = EXCLUDED.engine,
			voice               = EXCLUDED.voice,
			model               = EXCLUDED.model,
			segment_chars       = EXCLUDED.segment_chars,
			source_content_hash = EXCLUDED.source_content_hash,
			file_id             = NULL,
			error               = '',
			total_chars         = EXCLUDED.total_chars,
			duration_ms         = 0,
			updated_at          = now()
		WHERE book_audiobooks.state IN ('ready', 'failed', 'canceled')
		RETURNING generation
	`
	var generation int
	err = tx.QueryRowContext(ctx, upsert,
		ab.BookID, string(model.AudiobookPending), ab.Engine, ab.Voice, ab.Model,
		ab.SegmentChars, ab.SourceContentHash, ab.TotalChars,
	).Scan(&generation)
	if err != nil {
		// No row came back from a statement that inserts or updates exactly
		// one: the conflicting row failed the terminal-state guard above.
		// The transaction rolls back in the defer, so the live run keeps its
		// plan.
		if dberr.IsNotFound(err) {
			return 0, ErrRunInProgress
		}
		return 0, fmt.Errorf("upsert audiobook: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM book_audiobook_segments WHERE book_id = $1`, ab.BookID); err != nil {
		return 0, fmt.Errorf("clear segments: %w", err)
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
			return 0, fmt.Errorf("insert segment %d: %w", s.Seq, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return generation, nil
}

func (r *BookAudiobookRepo) GetByBookID(ctx context.Context, bookID string) (model.Audiobook, error) {
	const q = `SELECT ` + audiobookCols + ` FROM book_audiobooks WHERE book_id = $1`

	var (
		ab    model.Audiobook
		state string
	)
	row := r.db.SQL.QueryRowContext(ctx, q, bookID)
	if err := row.Scan(
		&ab.BookID, &state, &ab.Generation, &ab.Engine, &ab.Voice, &ab.Model,
		&ab.SegmentChars, &ab.SourceContentHash,
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

// MarkSegmentRunning claims a segment of one generation of a run.
// Reports whether the claim landed, which it does not for a segment
// already done — re-synthesizing that is a second bill for audio already
// paid for — nor for a job whose generation is not the run's.
//
// The generation is read from the parent row rather than from a column on
// the segment. Segments are wiped and re-planned on every Start, so a
// segment row can never disagree with its run, and a copy here would be a
// second place to be wrong (ADR-0031). The join is what makes the guard
// part of the same locked write as the claim: there is no read-then-check
// for a regeneration to slip between.
func (r *BookAudiobookRepo) MarkSegmentRunning(
	ctx context.Context, bookID string, seq, generation int,
) (bool, error) {
	const q = `
		UPDATE book_audiobook_segments s
		SET state = 'running', error = '', updated_at = now()
		FROM book_audiobooks r
		WHERE s.book_id = r.book_id
		  AND s.book_id = $1 AND s.seq = $2
		  AND r.generation = $3
		  AND s.state <> 'done'
	`
	res, err := r.db.SQL.ExecContext(ctx, q, bookID, seq, generation)
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
//
// Returns ErrNotFound when there is no run for the book, when the seq is
// not in the run's plan, and when the generation is not the run's: a
// write that moved nothing has no landing to derive a transition from.
//
// The generation is guarded here and not only at the claim, and that is
// correctness rather than belt-and-braces. A segment is claimed *before*
// synthesis and synthesis runs for minutes, so the window a regeneration
// can land in is the whole engine call — a job can claim under generation
// 1 and arrive here after generation 2 has started. Guarding the claim
// alone leaves that window wide open, which is the one that matters,
// since by then the audio exists and would land in the live plan's row.
func (r *BookAudiobookRepo) RecordSegment(
	ctx context.Context,
	bookID string,
	seq, generation int,
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
		UPDATE book_audiobook_segments s
		SET state = $4, staged_path = $5, duration_ms = $6, error = $7, updated_at = now()
		FROM book_audiobooks r
		WHERE s.book_id = r.book_id
		  AND s.book_id = $1 AND s.seq = $2
		  AND r.generation = $3
	`
	written, err := tx.ExecContext(ctx, writeSeg, bookID, seq, generation,
		string(res.State), res.StagedPath, res.DurationMS, res.Error)
	if err != nil {
		return zero, fmt.Errorf("record segment %d: %w", seq, err)
	}
	// A write that matched no row is a result addressed to a plan that is
	// not the one that exists — either a seq this run does not have, or a
	// generation a regeneration has moved past. Committing it would report
	// a transition derived from coverage the result never entered, for a
	// plan it never wrote to, so the call is refused with the sentinel a
	// missing run row already gets. The rollback in the defer releases the
	// lock (#220, #253).
	n, err := written.RowsAffected()
	if err != nil {
		return zero, fmt.Errorf("record segment %d: %w", seq, err)
	}
	if n == 0 {
		return zero, ErrNotFound
	}

	cov, err := scanCoverage(ctx, tx, bookID)
	if err != nil {
		return zero, fmt.Errorf("coverage for %s: %w", bookID, err)
	}

	// The transition is decided here, under the lock, and carried out by
	// the caller. Writing the failed state inside this transaction made
	// the repo the second of four places that marked a run failed; now it
	// reports what follows and AudiobookService applies it (#190). What
	// the lock is for survives: the state read, the segment write and the
	// coverage read are one atomic view, so two segments landing together
	// cannot both see themselves as last.
	next := model.NextForRun(model.AudiobookState(state), cov)

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

// Transition moves a run's state, but only out of one of the states the
// caller says it expects, and reports whether the row actually moved.
//
// The one write that *moves* a run. There were four before, three of them
// reached from a different module and two of them unguarded, and the
// unguarded pair is what let a late write undo a conclusion — a finalize
// in flight marking a cancelled run ready, and a trailing `running` write
// putting back a run a segment had already moved forward (#210).
//
// Start is the only other statement that writes the column, and it does
// not move a run: it establishes one, and it is guarded on the same kind
// of predicate — it takes the row only when there is none or the one
// there has concluded (ErrRunInProgress). Every other write in this
// module goes through here.
//
// The moved flag is the other half: it is how the SSE publish fires
// exactly once when two segments settle a run at the same instant.
//
// The guard itself is not stated here. `state = ANY($6::text[])` is
// array membership over model.Transition.FromStrings — the SQL rendering
// of model.Transition.Admits, which is the same question the service's
// test double asks. TestTransitionGuardAgreesWithTheModel holds this
// predicate to that method for every declared state, so the two cannot
// drift apart unnoticed (#233).
func (r *BookAudiobookRepo) Transition(
	ctx context.Context, bookID string, t model.Transition,
) (bool, error) {
	const q = `
		UPDATE book_audiobooks
		SET state       = $2,
		    error       = $3,
		    file_id     = COALESCE($4::uuid, file_id),
		    duration_ms = COALESCE($5, duration_ms),
		    updated_at  = now()
		WHERE book_id = $1 AND state = ANY($6::text[])
	`
	res, err := r.db.SQL.ExecContext(ctx, q,
		bookID, string(t.To), t.Error, t.FileID, t.DurationMS, t.FromStrings())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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
