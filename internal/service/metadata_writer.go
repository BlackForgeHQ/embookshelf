package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/model"
)

// Trigger identifies the upstream action that drove a metadata
// write. Different triggers cause different steps to fire in the
// pipeline; the per-step gating lives in MetadataWriter.Write.
type Trigger string

const (
	// TriggerManualEdit is set by the manual edit-metadata UI
	// handler. Fires DB + sidecar + file (gated by backend kind).
	TriggerManualEdit Trigger = "manual_edit"
	// TriggerApplyEnrichment is set by the apply-match UI flow.
	// Same coverage as TriggerManualEdit — explicit user intent.
	TriggerApplyEnrichment Trigger = "apply_enrichment"
	// TriggerAutoEnrichment is set by the headless auto-enrichment
	// background worker. Fires DB only — no sidecar/file write to
	// avoid stampedes on bulk auto-applies.
	TriggerAutoEnrichment Trigger = "auto_enrichment"
)

// BookMetadataWriter is the slice of *repo.BookRepo MetadataWriter
// needs. Defined here so tests can fake it without standing up a DB.
type BookMetadataWriter interface {
	UpdateMetadata(ctx context.Context, b model.Book) error
}

// MetadataWriterDeps groups the dependencies MetadataWriter needs.
// LibStore + Sidecar are nil-tolerant for the auto-enrichment-only
// case (DB write succeeds without them).
type MetadataWriterDeps struct {
	Books BookMetadataWriter
	// LibStore + Sidecar wired in Tasks 3-5. Nil here in this task.
}

// MetadataWriter coordinates the DB → sidecar → file pipeline for
// user-driven book metadata edits. Caller invokes Write with a
// fully-formed model.Book (the new desired state) and a Trigger
// value; the writer figures out which side-effect steps fire.
type MetadataWriter struct {
	deps MetadataWriterDeps
}

func NewMetadataWriter(deps MetadataWriterDeps) *MetadataWriter {
	return &MetadataWriter{deps: deps}
}

// Write persists the book's edited metadata. Returns nil after the
// DB step (step 1) succeeds; subsequent steps (sidecar, file) are
// post-commit best-effort and their failures are logged via slog.
//
// Trigger gating:
//   - manual_edit / apply_enrichment → DB + sidecar + file
//   - auto_enrichment → DB only
func (w *MetadataWriter) Write(ctx context.Context, b model.Book, trigger Trigger) error {
	if w.deps.Books == nil {
		return errors.New("metadata writer: no book repo configured")
	}

	if err := w.deps.Books.UpdateMetadata(ctx, b); err != nil {
		return fmt.Errorf("metadata writer: db: %w", err)
	}

	if trigger == TriggerAutoEnrichment {
		return nil
	}

	// Steps 2-3 land in Tasks 3-5. Stub for now — no-op logged.
	slog.Debug("metadata writer: post-commit steps stubbed",
		"book_id", b.ID, "trigger", string(trigger))
	return nil
}
