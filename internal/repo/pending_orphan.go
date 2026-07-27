// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
)

// ReasonOrphanRename is the reason value written by the S3 folder
// rename pipeline (ADR-0005).
const ReasonOrphanRename = "rename"

// ReasonOrphanBookDelete is written when a book is deleted from a
// backend-backed library. The bytes are queued rather than removed
// inline for the same reason a rename defers: a presigned URL already
// handed to a browser would otherwise 404 mid-download.
const ReasonOrphanBookDelete = "book_delete"

// PendingOrphanRepo provides write/select access to the
// pending_orphans table.
type PendingOrphanRepo struct {
	db *db.DB
}

// NewPendingOrphanRepo constructs a PendingOrphanRepo backed by d.
func NewPendingOrphanRepo(d *db.DB) *PendingOrphanRepo {
	return &PendingOrphanRepo{db: d}
}

// PendingOrphanInsert is one row to enqueue. EligibleAt + Reason +
// LibraryID are required; BookID may be nil.
type PendingOrphanInsert struct {
	LibraryID  string
	Key        string
	EligibleAt time.Time
	Reason     string
	BookID     *string
}

// Insert enqueues rows. Conflict on (library_id, key) is treated as
// "already enqueued" and silently skipped — a re-run of the same
// rename should not error.
func (r *PendingOrphanRepo) Insert(ctx context.Context, rows []PendingOrphanInsert) error {
	if len(rows) == 0 {
		return nil
	}
	q, args := buildPendingOrphanInsert(rows)
	_, err := r.db.SQL.ExecContext(ctx, q, args...)
	return err
}

// insertPendingOrphansInTx is the tx-bound counterpart to
// PendingOrphanRepo.Insert. Used by the BookRepo rename-folder tx so
// orphan inserts commit (or roll back) atomically with the
// files+books updates.
func insertPendingOrphansInTx(ctx context.Context, tx *sql.Tx, rows []PendingOrphanInsert) error {
	if len(rows) == 0 {
		return nil
	}
	q, args := buildPendingOrphanInsert(rows)
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

func buildPendingOrphanInsert(rows []PendingOrphanInsert) (string, []any) {
	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*5)
	for i, row := range rows {
		base := i * 5
		placeholders = append(placeholders, fmt.Sprintf(
			"($%d, $%d, $%d, $%d, $%d)",
			base+1, base+2, base+3, base+4, base+5,
		))
		args = append(args, row.LibraryID, row.Key, row.EligibleAt, row.Reason, row.BookID)
	}
	q := `
		INSERT INTO pending_orphans (library_id, key, eligible_at, reason, book_id)
		VALUES ` + strings.Join(placeholders, ", ") + `
		ON CONFLICT (library_id, key) DO NOTHING
	`
	return q, args
}

// SelectDue returns up to limit rows whose EligibleAt has passed,
// ordered by EligibleAt ASC. Used by the sweeper to pull a batch
// of work each tick.
func (r *PendingOrphanRepo) SelectDue(ctx context.Context, now time.Time, limit int) ([]model.PendingOrphan, error) {
	const q = `
		SELECT id, library_id, key, eligible_at, reason, book_id, created_at
		FROM pending_orphans
		WHERE eligible_at <= $1
		ORDER BY eligible_at ASC
		LIMIT $2
	`
	rows, err := r.db.SQL.QueryContext(ctx,
		q,
		now, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.PendingOrphan
	for rows.Next() {
		var (
			po           model.PendingOrphan
			bookIDNullSt *string
		)
		if err := rows.Scan(&po.ID, &po.LibraryID, &po.Key, &po.EligibleAt, &po.Reason, &bookIDNullSt, &po.CreatedAt); err != nil {
			return nil, err
		}
		po.BookID = bookIDNullSt
		out = append(out, po)
	}
	return out, rows.Err()
}

// Delete removes a row by id. Used by the sweeper after the key has
// been deleted from the backend (or confirmed already gone).
// Deleting a non-existent id is not an error — concurrent sweepers
// or operator deletes can race; the absence is the desired state.
func (r *PendingOrphanRepo) Delete(ctx context.Context, id int64) error {
	const q = `DELETE FROM pending_orphans WHERE id = $1`
	_, err := r.db.SQL.ExecContext(ctx, q, id)
	return err
}
