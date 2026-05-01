package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
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

// LibraryStoreFor is the slice of LibraryStore we depend on.
// Avoids a hard import of *defaultLibraryStore so tests can fake it.
type LibraryStoreFor interface {
	For(ctx context.Context, libraryID string) (*LibraryHandle, error)
}

// SidecarWriterFor is the slice of *sidecar.Writer we depend on.
// Mirrors the Plan 1 signature exactly.
type SidecarWriterFor interface {
	Write(ctx context.Context, store storage.Storage, key string, s sidecar.Sidecar, mode sidecar.WriteMode, format string) error
}

// EmbedderDispatcher is the slice of fileproc.DispatchEmbedder we
// depend on. Default impl wraps fileproc.DispatchEmbedder; tests
// inject a fake.
type EmbedderDispatcher func(format string) (fileproc.Embedder, error)

// MetadataWriterDeps groups the dependencies MetadataWriter needs.
// LibStore + Sidecar are nil-tolerant for the auto-enrichment-only
// case (DB write succeeds without them).
type MetadataWriterDeps struct {
	Books    BookMetadataWriter
	LibStore LibraryStoreFor
	Sidecar  SidecarWriterFor
	Dispatch EmbedderDispatcher
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

	mode := sidecar.ModeFull
	if w.tryEmbedFile(ctx, b) {
		mode = sidecar.ModeSpillover
	}
	w.writeSidecar(ctx, b, mode)
	return nil
}

// tryEmbedFile attempts the in-file write step. Returns true when
// the write succeeded; false (with side effects logged) when the
// step was skipped, the format is unsupported, or the embed/Put
// failed. Spillover-vs-full sidecar mode flows from this return.
func (w *MetadataWriter) tryEmbedFile(ctx context.Context, b model.Book) bool {
	if w.deps.LibStore == nil || w.deps.Dispatch == nil {
		return false
	}
	handle, err := w.deps.LibStore.For(ctx, b.LibraryID)
	if err != nil {
		slog.Warn("metadata writer: lib store lookup", "book_id", b.ID, "err", err)
		return false
	}
	if !handle.CanWriteInFile() || handle.Storage == nil {
		return false
	}
	emb, err := w.deps.Dispatch(b.Format)
	if err != nil {
		return false
	}
	src, err := handle.Storage.Open(ctx, b.Path)
	if err != nil {
		slog.Warn("metadata writer: open source", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	defer func() { _ = src.Close() }()
	in := fileproc.EmbedInput{
		Title:         b.Title,
		Subtitle:      b.Subtitle,
		Author:        b.Author,
		Description:   b.Description,
		Language:      b.Language,
		Publisher:     b.Publisher,
		PublishedDate: dateString(b.PublishDate),
		ISBN:          b.ISBN,
		Series:        b.Series,
		SeriesIndex:   b.SeriesIndex,
		Tags:          b.Tags,
		Genres:        b.Genres,
	}
	out, err := emb.Embed(ctx, src, in)
	if err != nil {
		slog.Warn("metadata writer: embed", "book_id", b.ID, "format", b.Format, "err", err)
		return false
	}
	if _, err := handle.Storage.Put(ctx, b.Path, bytes.NewReader(out)); err != nil {
		slog.Warn("metadata writer: put", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	return true
}

// writeSidecar persists the JSON sidecar. mode is decided by the
// caller per the spillover-vs-full rule. For Phase 3 we always use
// ModeFull because the file step is still stubbed (Task 4 flips this
// to spillover when the file step succeeds).
func (w *MetadataWriter) writeSidecar(ctx context.Context, b model.Book, mode sidecar.WriteMode) {
	if w.deps.LibStore == nil || w.deps.Sidecar == nil {
		return
	}
	handle, err := w.deps.LibStore.For(ctx, b.LibraryID)
	if err != nil {
		slog.Warn("metadata writer: lib store lookup", "book_id", b.ID, "err", err)
		return
	}
	key := handle.SidecarKey(b.Path)
	side := sidecar.Sidecar{
		Title:         b.Title,
		Subtitle:      b.Subtitle,
		Author:        b.Author,
		Description:   b.Description,
		Language:      b.Language,
		Publisher:     b.Publisher,
		PublishedDate: dateString(b.PublishDate),
		ISBN:          b.ISBN,
		Series:        b.Series,
		SeriesIndex:   b.SeriesIndex,
		Tags:          b.Tags,
		Genres:        b.Genres,
	}
	if err := w.deps.Sidecar.Write(ctx, handle.Storage, key, side, mode, b.Format); err != nil {
		slog.Warn("metadata writer: sidecar write", "book_id", b.ID, "key", key, "err", err)
	}
}

// dateString formats a *time.Time for the sidecar's PublishedDate
// string field. Returns "" when t is nil.
func dateString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}
