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
// rename pipeline (ADR-0005). Future reasons (delete, library-removed)
// will live alongside this constant.
const ReasonOrphanRename = "rename"

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
	switch r.db.Dialect {
	case db.DialectSQLite:
		return r.insertSQLite(ctx, rows)
	default:
		return r.insertPG(ctx, rows)
	}
}

func (r *PendingOrphanRepo) insertPG(ctx context.Context, rows []PendingOrphanInsert) error {
	q, args := buildPendingOrphanInsertPG(rows)
	_, err := r.db.SQL.ExecContext(ctx, q, args...)
	return err
}

func (r *PendingOrphanRepo) insertSQLite(ctx context.Context, rows []PendingOrphanInsert) error {
	q, args := buildPendingOrphanInsertSQLite(rows)
	_, err := r.db.SQL.ExecContext(ctx, q, args...)
	return err
}

// insertPendingOrphansInTx is the tx-bound counterpart to
// PendingOrphanRepo.Insert. Used by the BookRepo rename-folder tx so
// orphan inserts commit (or roll back) atomically with the
// files+books updates.
func insertPendingOrphansInTx(ctx context.Context, tx *sql.Tx, dialect db.Dialect, rows []PendingOrphanInsert) error {
	if len(rows) == 0 {
		return nil
	}
	var (
		q    string
		args []any
	)
	if dialect == db.DialectSQLite {
		q, args = buildPendingOrphanInsertSQLite(rows)
	} else {
		q, args = buildPendingOrphanInsertPG(rows)
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

func buildPendingOrphanInsertPG(rows []PendingOrphanInsert) (string, []any) {
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

func buildPendingOrphanInsertSQLite(rows []PendingOrphanInsert) (string, []any) {
	placeholders := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*5)
	for _, row := range rows {
		placeholders = append(placeholders, "(?, ?, ?, ?, ?)")
		args = append(args,
			row.LibraryID,
			row.Key,
			row.EligibleAt.UTC().Format(time.RFC3339Nano),
			row.Reason,
			row.BookID,
		)
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
	const qPG = `
		SELECT id, library_id, key, eligible_at, reason, book_id, created_at
		FROM pending_orphans
		WHERE eligible_at <= $1
		ORDER BY eligible_at ASC
		LIMIT $2
	`
	const qSQLite = `
		SELECT id, library_id, key, eligible_at, reason, book_id, created_at
		FROM pending_orphans
		WHERE eligible_at <= ?
		ORDER BY eligible_at ASC
		LIMIT ?
	`
	var nowArg any
	if r.db.Dialect == db.DialectSQLite {
		nowArg = now.UTC().Format(time.RFC3339Nano)
	} else {
		nowArg = now
	}
	rows, err := r.db.SQL.QueryContext(ctx,
		db.SelectQ(r.db.Dialect, qPG, qSQLite),
		nowArg, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.PendingOrphan
	for rows.Next() {
		var (
			po           model.PendingOrphan
			eligibleAny  any
			createdAny   any
			bookIDNullSt *string
		)
		if err := rows.Scan(&po.ID, &po.LibraryID, &po.Key, &eligibleAny, &po.Reason, &bookIDNullSt, &createdAny); err != nil {
			return nil, err
		}
		if err := db.ScanTime(r.db.Dialect, eligibleAny, &po.EligibleAt); err != nil {
			return nil, fmt.Errorf("scan eligible_at: %w", err)
		}
		if err := db.ScanTime(r.db.Dialect, createdAny, &po.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan created_at: %w", err)
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
	const qPG = `DELETE FROM pending_orphans WHERE id = $1`
	const qSQLite = `DELETE FROM pending_orphans WHERE id = ?`
	_, err := r.db.SQL.ExecContext(ctx, db.SelectQ(r.db.Dialect, qPG, qSQLite), id)
	return err
}
