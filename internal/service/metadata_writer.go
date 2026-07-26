// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// RenameRollbackGrace is the grace window applied to half-rename
// new keys that need to be reaped after a phase-1 copy or DB-tx
// failure. Short by design: no client ever held a presigned URL for
// these keys (DB never referenced them), so there's nothing to wait
// for. ADR-0005 §3.4.
const RenameRollbackGrace = 5 * time.Minute

// CopyRetryAttempts is the bounded retry budget for a single
// CopyObject during phase-1. Three attempts with exponential backoff
// rides out transient 5xx / throttle responses without spinning on a
// genuinely-broken backend.
const CopyRetryAttempts = 3

// CopyRetryBaseDelay is the first backoff delay between copy retries.
// Doubles each attempt: 200ms, 400ms, 800ms.
const CopyRetryBaseDelay = 200 * time.Millisecond

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
	SetFolderPath(ctx context.Context, bookID, folderPath, path string) error
	// RenameFolderTx is the single-transaction DB swap that finalises
	// an S3 folder rename per ADR-0005: rewrites every files.location
	// supplied, sets books.folder_path + books.path, and enqueues the
	// supplied orphan rows.
	RenameFolderTx(ctx context.Context, args repo.RenameFolderTxArgs) error
}

// PendingOrphansEnqueuer is the slice of *repo.PendingOrphanRepo
// MetadataWriter needs for the rollback path: a phase-1 copy or DB
// tx failure schedules the half-rename garbage with a short grace.
type PendingOrphansEnqueuer interface {
	Insert(ctx context.Context, rows []repo.PendingOrphanInsert) error
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

// FileMetadataRepo is the slice of *repo.FileRepo we depend on.
// Defined here so tests can fake it.
type FileMetadataRepo interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
	UpdateLocation(ctx context.Context, fileID, newLocation string) error
}

// MetadataWriterDeps groups the dependencies MetadataWriter needs.
// LibStore + Sidecar are nil-tolerant for the auto-enrichment-only
// case (DB write succeeds without them).
type MetadataWriterDeps struct {
	Books    BookMetadataWriter
	LibStore LibraryStoreFor
	Sidecar  SidecarWriterFor
	Dispatch EmbedderDispatcher
	Files    FileMetadataRepo
	// Orphans is the queue used by the S3 folder-rename rollback
	// path (ADR-0005). Nil disables backend rename — a degraded
	// MetadataWriter behaves like the pre-ADR-0005 build.
	Orphans PendingOrphansEnqueuer
	// RenameGrace is the eligible_at delta applied to the *old*
	// keys enqueued after a successful S3 rename. Defaults to 1h
	// when zero. Operators set this to ≥ 2 × PresignTTL.
	RenameGrace time.Duration
}

// Outcome reports the post-execution facts of a Write call. Tests
// pin behavior on it; SSE telemetry / audit may consume it later.
// Callers that don't need it can discard.
type Outcome struct {
	// InFileWritten is true when the in-file embedded write step
	// completed successfully (Embed + Put both ok). Drives
	// SidecarMode per ADR-0001's "inFileWritten == false → full
	// mirror" rule.
	InFileWritten bool
	// SidecarMode reports the mode used for the sidecar write step
	// (ModeFull or ModeSpillover). Empty when the sidecar step was
	// not part of the plan (e.g. auto-enrichment trigger).
	SidecarMode sidecar.WriteMode
	// FolderRenamed is true when the on-disk folder for the Book
	// was successfully moved to its new {Author}/{Title} location
	// per ADR-0003 §6.
	FolderRenamed bool
	// NewFolderPath holds the post-rename library-relative folder
	// when FolderRenamed is true. Empty otherwise.
	NewFolderPath string
}

// MetadataWriter is the **edit-side write pipeline** module. Owns
// ADR-0001's `DB → JSON sidecar → file embedded` sequence for the
// three edit-side triggers (manual_edit, apply_enrichment,
// auto_enrichment). Approve and scan-reingest deliberately route
// around this module — for those, the file IS the source so
// rewriting it would loop. The matrix lives in DecideEffects (pure);
// Write is a flat executor of that plan.
type MetadataWriter struct {
	deps MetadataWriterDeps
}

func NewMetadataWriter(deps MetadataWriterDeps) *MetadataWriter {
	return &MetadataWriter{deps: deps}
}

// Write persists the book's edited metadata per the plan returned
// by DecideEffects. The DB step is mandatory and propagates errors;
// subsequent steps (sidecar, file embed, folder rename) are
// best-effort and their failures are logged via slog. Returns
// Outcome describing what actually fired so callers / tests can
// verify the post-state.
//
// Step order (ADR-0003 §6.5): DB → in-file embed (old path) →
// sidecar (old path) → folder rename → DB tx update of
// files.location + books.folder_path. Renaming last keeps the
// existing pipeline writes correct on disk; if the rename itself
// fails the file is in the right shape just at the old folder.
func (w *MetadataWriter) Write(ctx context.Context, b model.Book, trigger Trigger) (Outcome, error) {
	if w.deps.Books == nil {
		return Outcome{}, errors.New("metadata writer: no book repo configured")
	}

	if err := w.deps.Books.UpdateMetadata(ctx, b); err != nil {
		return Outcome{}, fmt.Errorf("metadata writer: db: %w", err)
	}

	handle := w.lookupHandle(ctx, b)
	folderChanged, oldFolder, newFolder := w.folderDelta(b)
	eff := DecideEffects(trigger, handle, folderChanged)
	out := Outcome{}

	if eff.InFile && w.deps.Dispatch != nil {
		out.InFileWritten = w.embedAndStamp(ctx, b, handle)
	}

	if eff.Sidecar && w.deps.Sidecar != nil {
		out.SidecarMode = sidecar.ModeFull
		if out.InFileWritten {
			out.SidecarMode = sidecar.ModeSpillover
		}
		w.writeSidecar(ctx, b, handle, out.SidecarMode)
	}

	if eff.FolderRename && handle != nil {
		if renamed, finalFolder := w.renameFolder(ctx, b, handle, oldFolder, newFolder); renamed {
			out.FolderRenamed = true
			out.NewFolderPath = finalFolder
		}
	}

	return out, nil
}

// folderDelta computes whether the Book's stored folder_path differs
// from the sanitized {Author}/{Title} path implied by its current
// metadata. Returns the delta flag plus both paths so callers can
// reuse the work without re-sanitizing.
//
// oldFolder is "" when the Book has never had a folder_path (legacy
// flat-layout Books). The lazy migration in ADR-0003 §5 picks these
// up on first edit by treating the empty string as "needs to land
// somewhere new."
func (w *MetadataWriter) folderDelta(b model.Book) (changed bool, oldFolder, newFolder string) {
	if b.FolderPath != nil {
		oldFolder = *b.FolderPath
	}
	newFolder = filepath.Join(layout.SanitizeAuthor(b.Author), layout.SanitizeTitle(b.Title))
	return oldFolder != newFolder, oldFolder, newFolder
}

// renameFolder dispatches the post-DB rename step by backend kind.
// Local: atomic os.Rename on the same FS (ADR-0003 §6.5). Backend
// (S3): list-prefix + server-side copy-loop + single-tx DB swap +
// sweeper-deferred delete (ADR-0005).
//
// On any failure the on-disk / backend state is left consistent
// with the pre-rename world; the DB metadata is already committed
// (sidecar carries it on S3) and the caller can retry.
func (w *MetadataWriter) renameFolder(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) (bool, string) {
	if handle.IsBackendBacked() {
		return w.renameFolderBackend(ctx, b, handle, oldFolder, newFolder)
	}
	return w.renameFolderLocal(ctx, b, handle, oldFolder, newFolder)
}

// renameFolderLocal is the local-fs arm of renameFolder. Two cases:
//   - Legacy flat-layout (oldFolder == ""): files sit at library
//     root. We move only this Book's files (per files.location),
//     preserving siblings that belong to other flat-layout Books.
//   - New layout (oldFolder != ""): the Book owns a folder. We
//     rename the directory wholesale via os.Rename — atomic on
//     same FS, carries the sidecar and any companion files for
//     free.
func (w *MetadataWriter) renameFolderLocal(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) (bool, string) {
	libRoot := strings.TrimRight(handle.Library.Path, "/")
	if libRoot == "" {
		slog.Warn("metadata writer: rename folder skipped (no library root)", "book_id", b.ID)
		return false, ""
	}

	finalFolder := newFolder
	finalAbs := filepath.Join(libRoot, finalFolder)
	if oldFolder != "" {
		// Whole-folder move. Build a unique destination dir if the
		// target already exists (collision with another Book that
		// happens to share Author+Title).
		oldAbs := filepath.Join(libRoot, oldFolder)
		finalAbs = uniqueDirectoryUnless(finalAbs, oldAbs)
		finalFolder = strings.TrimPrefix(finalAbs, libRoot+"/")

		if err := os.MkdirAll(filepath.Dir(finalAbs), 0o755); err != nil {
			slog.Warn("metadata writer: mkdir parent for rename",
				"book_id", b.ID, "parent", filepath.Dir(finalAbs), "err", err)
			return false, ""
		}
		if err := os.Rename(oldAbs, finalAbs); err != nil {
			slog.Warn("metadata writer: rename folder",
				"book_id", b.ID, "from", oldAbs, "to", finalAbs, "err", err)
			return false, ""
		}
	} else {
		// Legacy flat-layout: move only this Book's files into the
		// new folder. Sibling flat-layout files belong to other
		// Books and must not be touched.
		finalAbs = uniqueDirectory(finalAbs)
		finalFolder = strings.TrimPrefix(finalAbs, libRoot+"/")
		if err := os.MkdirAll(finalAbs, 0o755); err != nil {
			slog.Warn("metadata writer: mkdir new folder",
				"book_id", b.ID, "dir", finalAbs, "err", err)
			return false, ""
		}
		if !w.moveFlatFiles(ctx, b, libRoot, finalAbs) {
			return false, ""
		}
	}

	if !w.persistRename(ctx, b, finalFolder, libRoot) {
		// Persist failed after the on-disk move. The DB still says
		// "old folder", reality is "new folder". This is a known
		// soft-failure; surfaces in scan reattach via content hash.
		return false, ""
	}
	return true, finalFolder
}

// moveFlatFiles handles the lazy-migration case for a legacy
// flat-layout Book. Moves every files row's on-disk entry from
// `{libRoot}/{basename}` into `{newDir}/{basename}` so we don't
// scoop up siblings that belong to other Books.
func (w *MetadataWriter) moveFlatFiles(
	ctx context.Context,
	b model.Book,
	libRoot, newDir string,
) bool {
	if w.deps.Files == nil {
		// Without a files repo we can't enumerate Book files
		// independently. Fall back to "move just b.Path".
		return w.moveSingleFile(b, libRoot, newDir)
	}
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		slog.Warn("metadata writer: list files for flat move",
			"book_id", b.ID, "err", err)
		return false
	}
	if len(files) == 0 {
		return w.moveSingleFile(b, libRoot, newDir)
	}
	for _, f := range files {
		// Flat-layout files live directly under the library root
		// with location = filename. Bail if a non-flat file shows
		// up — should not happen but keeps us honest.
		if strings.Contains(f.Location, "/") {
			slog.Warn("metadata writer: skipping non-flat file row in flat-rename",
				"book_id", b.ID, "location", f.Location)
			continue
		}
		from := filepath.Join(libRoot, f.Location)
		to := filepath.Join(newDir, f.Location)
		if err := moveFile(from, to); err != nil {
			slog.Warn("metadata writer: move flat file",
				"book_id", b.ID, "from", from, "to", to, "err", err)
			return false
		}
	}
	return true
}

// moveSingleFile is the no-files-repo fallback for the flat-layout
// rename: move just the Book's primary file (b.Path) into the new
// folder.
func (w *MetadataWriter) moveSingleFile(b model.Book, libRoot, newDir string) bool {
	base := filepath.Base(b.Path)
	if base == "" || base == "." || base == "/" {
		return false
	}
	from := filepath.Join(libRoot, b.Path)
	if filepath.IsAbs(b.Path) {
		from = b.Path
	}
	to := filepath.Join(newDir, base)
	if err := moveFile(from, to); err != nil {
		slog.Warn("metadata writer: move single flat file",
			"book_id", b.ID, "from", from, "to", to, "err", err)
		return false
	}
	return true
}

// persistRename updates files.location for every files row of the
// Book and books.folder_path + books.path so the DB reflects the
// post-rename layout. Best-effort: per-file failures log and
// continue; the books-row update is the last write so a crash
// midway leaves only file rows pointing at stale locations, which
// scan reattach corrects on next pass.
func (w *MetadataWriter) persistRename(
	ctx context.Context,
	b model.Book,
	newFolder, libRoot string,
) bool {
	if w.deps.Files != nil {
		files, err := w.deps.Files.ListByBook(ctx, b.ID)
		if err != nil {
			slog.Warn("metadata writer: list files post-rename",
				"book_id", b.ID, "err", err)
		}
		for _, f := range files {
			newLoc := path.Join(newFolder, filepath.Base(f.Location))
			if err := w.deps.Files.UpdateLocation(ctx, f.ID, newLoc); err != nil {
				slog.Warn("metadata writer: update files.location",
					"book_id", b.ID, "file_id", f.ID, "err", err)
			}
		}
	}
	newBookPath := path.Join(newFolder, filepath.Base(b.Path))
	if err := w.deps.Books.SetFolderPath(ctx, b.ID, newFolder, newBookPath); err != nil {
		slog.Warn("metadata writer: set folder_path",
			"book_id", b.ID, "folder", newFolder, "err", err)
		return false
	}
	return true
}

// renameFolderBackend is the S3 (any non-local Storage) arm of
// renameFolder per ADR-0005. Pipeline:
//
//  1. List-prefix the old folder to enumerate every key (files +
//     sidecar + cover + any user artifacts).
//  2. Resolve a non-colliding new prefix via uniqueBackendFolder.
//  3. Copy each old key to the new prefix, with bounded retry. On
//     mid-loop failure, schedule the new keys we already wrote with
//     RenameRollbackGrace and bail.
//  4. Compute the per-files location updates for the rows DB knows
//     about (others ride along under the new prefix without a row).
//  5. RenameFolderTx: single transaction wraps files updates +
//     books folder_path/path + INSERT pending_orphans (old keys,
//     RenameGrace).
//
// Inline phase-2 deletes do not happen here — the sweeper drains
// pending_orphans after the grace window. This keeps already-issued
// presigned URLs valid for at least 2× PresignTTL.
func (w *MetadataWriter) renameFolderBackend(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) (bool, string) {
	if handle.Storage == nil {
		slog.Warn("metadata writer: backend rename skipped (no storage)", "book_id", b.ID)
		return false, ""
	}
	if w.deps.Orphans == nil {
		// Without an orphan queue we cannot defer the source delete
		// safely. Fail closed — sidecar full-mirror still carries the
		// edit per ADR-0001's S3 fallback.
		slog.Warn("metadata writer: backend rename skipped (no orphan queue)",
			"book_id", b.ID)
		return false, ""
	}
	if oldFolder == "" {
		// S3 BackendPlacer has always written {Author}/{Title} prefixes
		// (ADR-0003 §7) so a folder_path of "" on an S3-backed Book is
		// either a pre-storage_v2 row or data corruption. Either way
		// we cannot list the source — bail without a copy attempt.
		slog.Warn("metadata writer: backend rename skipped (no source folder)",
			"book_id", b.ID)
		return false, ""
	}

	finalFolder := uniqueBackendFolder(ctx, handle.Storage, newFolder)
	oldPrefix := oldFolder + "/"
	newPrefix := finalFolder + "/"

	srcKeys, err := listBackendPrefix(ctx, handle.Storage, oldPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename list source",
			"book_id", b.ID, "prefix", oldPrefix, "err", err)
		return false, ""
	}
	if len(srcKeys) == 0 {
		slog.Warn("metadata writer: backend rename source is empty",
			"book_id", b.ID, "prefix", oldPrefix)
		return false, ""
	}

	copied := make([]string, 0, len(srcKeys))
	for _, src := range srcKeys {
		dst := newPrefix + strings.TrimPrefix(src, oldPrefix)
		if err := copyWithRetry(ctx, handle.Storage, src, dst); err != nil {
			slog.Warn("metadata writer: backend rename copy",
				"book_id", b.ID, "src", src, "dst", dst, "err", err)
			w.scheduleOrphans(ctx, handle.Library.ID, copied, RenameRollbackGrace, b.ID)
			return false, ""
		}
		copied = append(copied, dst)
	}

	fileUpdates, err := w.collectFileLocationUpdates(ctx, b, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename file enumeration",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, copied, RenameRollbackGrace, b.ID)
		return false, ""
	}

	newBookPath := newPrefix + filepath.Base(b.Path)
	orphanInserts := buildOrphanInserts(handle.Library.ID, srcKeys, w.renameGrace(), b.ID)

	if err := w.deps.Books.RenameFolderTx(ctx, repo.RenameFolderTxArgs{
		BookID:    b.ID,
		NewFolder: finalFolder,
		NewPath:   newBookPath,
		Files:     fileUpdates,
		Orphans:   orphanInserts,
	}); err != nil {
		slog.Warn("metadata writer: backend rename db tx",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, copied, RenameRollbackGrace, b.ID)
		return false, ""
	}

	return true, finalFolder
}

// renameGrace returns the configured grace duration for old keys
// after a successful rename. Defaults to 1h when unset, matching
// the ADR-0005 fallback used in cmd/embookshelf wiring.
func (w *MetadataWriter) renameGrace() time.Duration {
	if w.deps.RenameGrace > 0 {
		return w.deps.RenameGrace
	}
	return time.Hour
}

// collectFileLocationUpdates lists the Book's files rows and builds
// per-row UPDATE inputs for any row whose location lives under the
// old prefix. Rows that don't (legacy data drift) are skipped — they
// will not survive the post-tx state but the rename should not fail
// over a misaligned row.
func (w *MetadataWriter) collectFileLocationUpdates(
	ctx context.Context,
	b model.Book,
	oldPrefix, newPrefix string,
) ([]repo.FileLocationUpdate, error) {
	if w.deps.Files == nil {
		return nil, nil
	}
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		return nil, err
	}
	out := make([]repo.FileLocationUpdate, 0, len(files))
	for _, f := range files {
		if !strings.HasPrefix(f.Location, oldPrefix) {
			slog.Warn("metadata writer: backend rename skipping mis-prefixed file",
				"book_id", b.ID, "file_id", f.ID, "location", f.Location)
			continue
		}
		out = append(out, repo.FileLocationUpdate{
			FileID:   f.ID,
			Location: newPrefix + strings.TrimPrefix(f.Location, oldPrefix),
		})
	}
	return out, nil
}

// scheduleOrphans is the rollback escape hatch: enqueue the supplied
// (presumably new-prefix) keys with RenameRollbackGrace so the
// sweeper deletes the half-rename garbage on its next pass. Best
// effort — failure to enqueue is logged but does not change the
// caller's error path.
func (w *MetadataWriter) scheduleOrphans(
	ctx context.Context,
	libraryID string,
	keys []string,
	grace time.Duration,
	bookID string,
) {
	if len(keys) == 0 || w.deps.Orphans == nil {
		return
	}
	rows := buildOrphanInserts(libraryID, keys, grace, bookID)
	if err := w.deps.Orphans.Insert(ctx, rows); err != nil {
		slog.Warn("metadata writer: schedule rollback orphans",
			"library_id", libraryID, "count", len(keys), "err", err)
	}
}

// buildOrphanInserts converts a slice of keys into the typed insert
// rows for the pending_orphans table. eligible_at is now+grace.
func buildOrphanInserts(libraryID string, keys []string, grace time.Duration, bookID string) []repo.PendingOrphanInsert {
	if len(keys) == 0 {
		return nil
	}
	now := time.Now().UTC()
	out := make([]repo.PendingOrphanInsert, 0, len(keys))
	bid := bookID
	for _, k := range keys {
		out = append(out, repo.PendingOrphanInsert{
			LibraryID:  libraryID,
			Key:        k,
			EligibleAt: now.Add(grace),
			Reason:     repo.ReasonOrphanRename,
			BookID:     &bid,
		})
	}
	return out
}

// listBackendPrefix walks store under prefix and returns every key
// found (paginated iterators handled by storage.Iterator). Returned
// keys preserve the prefix so callers can compute new keys via
// strings.TrimPrefix without re-joining.
func listBackendPrefix(ctx context.Context, store storage.Storage, prefix string) ([]string, error) {
	it, err := store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = it.Close() }()
	var keys []string
	for {
		obj, err := it.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

// copyWithRetry runs Storage.Copy with bounded exponential backoff.
// Context cancellation aborts immediately; other errors are retried
// up to CopyRetryAttempts times. Returns the final error on
// exhaustion. Backends that surface ErrNotFound are treated as
// terminal — there is nothing to retry on a missing source.
func copyWithRetry(ctx context.Context, store storage.Storage, src, dst string) error {
	delay := CopyRetryBaseDelay
	var lastErr error
	for attempt := 0; attempt < CopyRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err := store.Copy(ctx, src, dst)
		if err == nil {
			return nil
		}
		if errors.Is(err, storage.ErrNotFound) {
			return err
		}
		lastErr = err
		if attempt+1 < CopyRetryAttempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}
	}
	return lastErr
}

// uniqueDirectoryUnless is a variant of uniqueDirectory that returns
// the input unchanged when it equals the source (oldAbs). Used by
// rename to avoid bumping a target that is the same directory we're
// renaming from (a no-op rename) — though folderDelta should have
// short-circuited that case already.
func uniqueDirectoryUnless(dest, oldAbs string) string {
	if dest == oldAbs {
		return dest
	}
	return uniqueDirectory(dest)
}

// lookupHandle resolves the library handle once per Write call. nil
// is a valid return — DecideEffects degrades the plan accordingly.
func (w *MetadataWriter) lookupHandle(ctx context.Context, b model.Book) *LibraryHandle {
	if w.deps.LibStore == nil {
		return nil
	}
	h, err := w.deps.LibStore.For(ctx, b.LibraryID)
	if err != nil {
		slog.Warn("metadata writer: lib store lookup", "book_id", b.ID, "err", err)
		return nil
	}
	return h
}

// embedAndStamp runs the in-file embed step against an already
// resolved local handle (DecideEffects has guaranteed Storage != nil
// and BackendID == nil). Returns true on success; logs and returns
// false on any per-step failure (no-format-embedder, open, embed,
// put). Stamps files.content_hash on success when a Files repo is
// wired.
func (w *MetadataWriter) embedAndStamp(ctx context.Context, b model.Book, handle *LibraryHandle) bool {
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
	em := b.Editable()
	em.PublishedDate = dateString(b.PublishDate)
	in := fileproc.EmbedInput{EditableMetadata: em}
	out, err := emb.Embed(ctx, src, in)
	if err != nil {
		slog.Warn("metadata writer: embed", "book_id", b.ID, "format", b.Format, "err", err)
		return false
	}
	if _, err := handle.Storage.Put(ctx, b.Path, bytes.NewReader(out)); err != nil {
		slog.Warn("metadata writer: put", "book_id", b.ID, "path", b.Path, "err", err)
		return false
	}
	if w.deps.Files != nil {
		w.stampFileHash(ctx, b, out)
	}
	return true
}

// stampFileHash computes sha256 of the freshly-written file bytes
// and updates files.content_hash for the book's file row. Picker
// rules (1:1 in practice today; schema permits N>1):
//   - 0 rows:   no-op (backfill catches up).
//   - 1 row:    stamp it regardless of format.
//   - N>1 rows: stamp the row whose format matches the just-written
//     book.Format. If no match exists we refuse to guess and log a
//     loud warn — silent stamp of the wrong row would corrupt the
//     scan hash-stamp guard for that file.
func (w *MetadataWriter) stampFileHash(ctx context.Context, b model.Book, out []byte) {
	files, err := w.deps.Files.ListByBook(ctx, b.ID)
	if err != nil {
		slog.Warn("metadata writer: list files", "book_id", b.ID, "err", err)
		return
	}
	if len(files) == 0 {
		return
	}
	var target model.File
	if len(files) == 1 {
		target = files[0]
	} else {
		for _, f := range files {
			if f.Format == b.Format {
				target = f
				break
			}
		}
		if target.ID == "" {
			slog.Warn("metadata writer: stamp skipped (multi-row, no format match)",
				"book_id", b.ID, "format", b.Format, "rows", len(files))
			return
		}
	}
	sum := sha256.Sum256(out)
	if err := w.deps.Files.SetContentHash(ctx, target.ID, sum[:], int64(len(out)), time.Now().UTC()); err != nil {
		slog.Warn("metadata writer: set content hash", "file_id", target.ID, "err", err)
	}
}

// writeSidecar persists the JSON sidecar. mode is decided by the
// caller per ADR-0001's spillover-vs-full rule (set from
// Outcome.InFileWritten). handle is required (DecideEffects only
// schedules sidecar when Storage != nil); failures are logged.
func (w *MetadataWriter) writeSidecar(ctx context.Context, b model.Book, handle *LibraryHandle, mode sidecar.WriteMode) {
	key := handle.SidecarKey(b.Path)
	side := b.Editable()
	side.PublishedDate = dateString(b.PublishDate)
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
