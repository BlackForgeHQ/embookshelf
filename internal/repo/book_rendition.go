// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import (
	"context"
	"fmt"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
)

// renditionLifecycle is the one implementation of the four-state
// rendition lifecycle (model.RenditionState), shared by the markdown and
// EPUB tracking repos. Per-artifact code is a projection — which columns
// MarkReady writes and how GetByBookID scans — never a copy of the
// lifecycle: the two repos used to be ~90-line twins with four unguarded
// writes each (#296).
//
// Every state write is guarded on the states the model declares it may
// move the row out of (model.RenditionWrite), the book_audiobooks
// Transition shape. A write the guard refuses is a no-op returning nil —
// the row keeps its conclusion and the caller has nothing to act on — and
// a write with no row at all is ErrNotFound, as before.
type renditionLifecycle struct {
	db    *db.DB
	table string
}

// Start upserts the row to pending with a clean error channel — the one
// write allowed to reopen a sealed ready row, because it is the user
// asking for a regeneration. The last good artifact fields (location or
// file pointer, hash, version) survive until a new ready overwrites
// them, so a failed regeneration does not orphan the bytes a consumer
// may still be reading.
func (l renditionLifecycle) Start(ctx context.Context, bookID string) error {
	q := `
		INSERT INTO ` + l.table + ` (book_id, state)
		VALUES ($1, 'pending')
		ON CONFLICT (book_id) DO UPDATE SET
			state      = 'pending',
			error      = '',
			updated_at = now()
	`
	_, err := l.db.SQL.ExecContext(ctx, q, bookID)
	return err
}

// MarkRunning records that a worker picked the row up.
func (l renditionLifecycle) MarkRunning(ctx context.Context, bookID string) error {
	return l.write(ctx, bookID, model.RenditionRunning, `error = ''`)
}

// MarkFailed records why, verbatim — what lands here is exactly what the
// status API surfaces (ADR-0033 §5). On a ready row it is a refused
// no-op: a late failure from a superseded job must not overwrite a
// conclusion (TestRenditionReadyRowIsSealed).
func (l renditionLifecycle) MarkFailed(ctx context.Context, bookID, msg string) error {
	return l.write(ctx, bookID, model.RenditionFailed, `error = $3`, msg)
}

// markReady seals the row around the artifact projection: state, clean
// error and provenance are the lifecycle's; artifactSet names the
// columns only this artifact has, with placeholders from $5.
func (l renditionLifecycle) markReady(
	ctx context.Context, bookID string, sourceHash []byte, version string,
	artifactSet string, artifactArgs ...any,
) error {
	set := `error = '', source_content_hash = $3, converter_version = $4, ` + artifactSet
	args := append([]any{sourceHash, version}, artifactArgs...)
	return l.write(ctx, bookID, model.RenditionReady, set, args...)
}

// write renders one declared transition into SQL: $1 the book, $2 the
// destination state, set's own placeholders from $3, and the model's
// From set as the guard — the same rendering book_audiobooks.Transition
// uses, held to the model by TestRenditionGuardAgreesWithTheModel.
func (l renditionLifecycle) write(
	ctx context.Context, bookID string, to model.RenditionState, set string, args ...any,
) error {
	t := model.RenditionWrite(to)
	guard := len(args) + 3
	q := `UPDATE ` + l.table + ` SET state = $2, updated_at = now(), ` + set +
		fmt.Sprintf(` WHERE book_id = $1 AND state = ANY($%d::text[])`, guard)

	all := append([]any{bookID, string(t.To)}, args...)
	all = append(all, t.FromStrings())
	res, err := l.db.SQL.ExecContext(ctx, q, all...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n > 0 {
		return nil
	}

	// Nothing moved: either the guard refused a sealed row (no-op) or
	// there is no row at all (ErrNotFound, as every reader answers it).
	var exists bool
	if err := l.db.SQL.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+l.table+` WHERE book_id = $1)`, bookID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
