// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
)

type ProgressRepo struct {
	db *db.DB
}

func NewProgressRepo(db *db.DB) *ProgressRepo {
	return &ProgressRepo{db: db}
}

// LatestForBook returns the most recent last_read_at across all users
// for the given bookID. Returns a zero time.Time when no progress rows
// exist (book never opened).
func (r *ProgressRepo) LatestForBook(ctx context.Context, bookID string) (time.Time, error) {
	const q = `SELECT MAX(last_read_at) FROM user_book_progress WHERE book_id = $1`

	// MAX() over no rows is SQL NULL, so the destination has to be
	// nullable even though the column is not: a book nobody has opened
	// yields the zero time rather than an error.
	var latest *time.Time
	err := r.db.SQL.QueryRowContext(ctx, q, bookID).Scan(&latest)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	if latest == nil {
		return time.Time{}, nil
	}
	return *latest, nil
}

// audioLocatorPrefix marks a position inside a narration. The client
// encodes it (ui/src/lib/locator.ts), which is what lets the column be
// chosen from the value rather than from a flag the caller has to
// remember to set.
const audioLocatorPrefix = "time:"

// Set records a user's progress for a book.
//
// The locator is optional; an empty string leaves both previously-stored
// resume points intact, so a manual percent tweak does not wipe out a
// reader-recorded position.
//
// Which position it updates is decided by the locator's own kind. A
// narrated book is opened by two shells speaking different kinds — the
// text shell writes a CFI or a page, the audio shell writes a timestamp
// — and one column meant each overwrote the other, so Read → Listen →
// Read lost the reader's place in both directions (#200).
//
// progress stays shared deliberately: "how far through this book am I"
// is one question about the work however it was consumed, and it is what
// the library card shows. Until the alignment map bridges the two
// (ADR-0025 §3, ADR-0029 §4), the most recent activity is the best
// answer available.
func (r *ProgressRepo) Set(ctx context.Context, userID, bookID string, progress int, locator string) error {
	const q = `
		INSERT INTO user_book_progress (user_id, book_id, progress, resume_cfi, resume_audio, last_read_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id, book_id) DO UPDATE
		  SET progress     = EXCLUDED.progress,
		      resume_cfi   = CASE WHEN EXCLUDED.resume_cfi <> ''
		                          THEN EXCLUDED.resume_cfi
		                          ELSE user_book_progress.resume_cfi END,
		      resume_audio = CASE WHEN EXCLUDED.resume_audio <> ''
		                          THEN EXCLUDED.resume_audio
		                          ELSE user_book_progress.resume_audio END,
		      last_read_at = now()
	`
	var text, audio string
	if strings.HasPrefix(locator, audioLocatorPrefix) {
		audio = locator
	} else {
		text = locator
	}
	_, err := r.db.SQL.ExecContext(ctx, q, userID, bookID, progress, text, audio)
	return err
}

// Resume returns a user's two stored positions: where they were reading,
// and where they were listening.
func (r *ProgressRepo) Resume(ctx context.Context, userID, bookID string) (text, audio string, err error) {
	const q = `
		SELECT COALESCE(resume_cfi, ''), COALESCE(resume_audio, '')
		FROM user_book_progress
		WHERE user_id = $1 AND book_id = $2
	`
	err = r.db.SQL.QueryRowContext(ctx, q, userID, bookID).Scan(&text, &audio)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return text, audio, err
}
