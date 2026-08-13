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

// DuePendingOrphan is a due row plus the one fact about it the row
// cannot carry: whether anything still points at the key.
//
// A pending_orphans row records that a key *was* orphaned at some past
// moment. That is not the same claim as "the key is orphaned now", and
// the difference is the whole reason the flag exists — keys in this
// system are deterministic, so a later write can land on a key already
// queued for deletion (#273).
type DuePendingOrphan struct {
	model.PendingOrphan
	// Referenced reports that a live files row in the same library names
	// this key. The sweeper must not delete such a key: whatever was
	// abandoned here has since been written over by something a reader
	// can reach.
	Referenced bool
}

// duePendingOrphanProjection is the SelectDue row, declared once:
// pending_orphans' own six columns plus the EXISTS join that answers
// Referenced. library_id, key and reason are three adjacent TEXT
// columns — a crossed pair would compile, run, and hand the sweeper a
// row addressing the wrong library or the wrong key, which is exactly
// the write a delete-by-key sweep must not make against a key still in
// use.
var duePendingOrphanProjection = projection[DuePendingOrphan]{
	{name: "id", dest: func(o *DuePendingOrphan) any { return &o.ID }},
	{name: "library_id", dest: func(o *DuePendingOrphan) any { return &o.LibraryID }},
	{name: "key", dest: func(o *DuePendingOrphan) any { return &o.Key }},
	{name: "eligible_at", dest: func(o *DuePendingOrphan) any { return &o.EligibleAt }},
	{name: "reason", dest: func(o *DuePendingOrphan) any { return &o.Reason }},
	{name: "book_id", dest: func(o *DuePendingOrphan) any { return &o.BookID }},
	{name: "created_at", dest: func(o *DuePendingOrphan) any { return &o.CreatedAt }},
	{
		name: "referenced",
		expr: `EXISTS (
		           SELECT 1 FROM files f
		           WHERE f.library_id = {alias}.library_id AND f.location = {alias}.key
		       ) AS referenced`,
		dest: func(o *DuePendingOrphan) any { return &o.Referenced },
	},
}

// SelectDue returns up to limit rows whose EligibleAt has passed,
// ordered by EligibleAt ASC. Used by the sweeper to pull a batch
// of work each tick.
//
// The files lookup rides along in the same query rather than being a
// second call per row: the answer has to be as of the moment the batch
// is read, and 500 round-trips per tick to learn it would be a poor
// trade for a fact one EXISTS already knows. Matching is on
// (library_id, location) because on a backend-backed library — the only
// kind that enqueues here — files.location *is* the storage key, which
// is what DeleteBookBytes relies on when it queues locations verbatim.
func (r *PendingOrphanRepo) SelectDue(ctx context.Context, now time.Time, limit int) ([]DuePendingOrphan, error) {
	q := `
		SELECT ` + duePendingOrphanProjection.selectList("po") + `
		FROM pending_orphans po
		WHERE po.eligible_at <= $1
		ORDER BY po.eligible_at ASC
		LIMIT $2
	`
	rows, err := r.db.SQL.QueryContext(ctx,
		q,
		now, limit,
	)
	if err != nil {
		return nil, err
	}
	return collect(rows, nil, func(s scanner) (DuePendingOrphan, error) {
		var po DuePendingOrphan
		err := duePendingOrphanProjection.scan(s, &po)
		return po, err
	})
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
