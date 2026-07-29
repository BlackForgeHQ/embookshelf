// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
	// SidecarWritten is true when the sidecar step completed. Distinct
	// from SidecarMode being non-empty, which only ever meant "a mode
	// was planned".
	SidecarWritten bool
	// FolderRenamed is true when the on-disk folder for the Book
	// was successfully moved to its new {Author}/{Title} location
	// per ADR-0003 §6.
	FolderRenamed bool
	// NewFolderPath holds the post-rename library-relative folder
	// when FolderRenamed is true. Empty otherwise.
	NewFolderPath string
	// Failures records the steps that were planned and did not
	// complete: the sidecar and in-file writes, the two whose silent
	// loss costs the edit its portable copy. The DB step is never here
	// — it fails the call. Folder rename is not here either; see Write.
	Failures []StepFailure
}

// StepFailure names a write step that was planned and did not complete.
type StepFailure struct {
	// Step is the user-facing name of the step: "sidecar", "in-file
	// write", "folder rename".
	Step string
	Err  error
}

// Degraded reports whether any planned step after the DB write failed.
// A degraded write still persisted the edit — the books row is
// canonical — but the on-disk record is behind it.
func (o Outcome) Degraded() bool { return len(o.Failures) > 0 }

// Warnings renders the failures for a human. Callers put these on the
// response so the person who made the edit learns their change did not
// reach the file, instead of it only appearing in a server log.
func (o Outcome) Warnings() []string {
	if len(o.Failures) == 0 {
		return nil
	}
	out := make([]string, 0, len(o.Failures))
	for _, f := range o.Failures {
		out = append(out, fmt.Sprintf("%s not written: %v", f.Step, f.Err))
	}
	return out
}

// fail records a step failure on the outcome.
func (o *Outcome) fail(step string, err error) {
	o.Failures = append(o.Failures, StepFailure{Step: step, Err: err})
}

// MetadataWriter is the **edit-side write pipeline** module. Owns
// ADR-0001's `DB → file embedded → JSON sidecar` sequence for the
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

// Write persists the book's edited metadata per the plan returned by
// DecideEffects.
//
// Only the DB step fails the call. The later steps are best-effort, but
// "best-effort" is not "unreported": a nil error means the books row was
// updated and nothing more, so callers must consult Outcome to learn
// whether the sidecar and in-file copies kept up. Outcome.Degraded and
// Outcome.Warnings exist for exactly that, and the handlers put the
// warnings on the response — a user whose edit did not reach the file
// should not have to read server logs to find out.
//
// Step order (ADR-0003 §6.5): DB → in-file embed (old path) →
// sidecar (old path) → folder rename → DB tx update of
// files.location + books.folder_path. Renaming last keeps the
// existing pipeline writes correct on disk; if the rename itself
// fails the file is in the right shape just at the old folder.
//
// The embed must precede the sidecar: the sidecar's mode is chosen from
// whether the in-file write landed (ADR-0001's "inFileWritten == false →
// full mirror"). The module doc above this type, and CONTEXT.md, both
// used to give the order as DB → sidecar → embed, which cannot work.
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
		if err := w.embedAndStamp(ctx, b, handle); err != nil {
			// ADR-0001 §3 treats a failed in-file write as a reason to
			// fall back to a full-mirror sidecar rather than an error,
			// so this is a degradation and not a failure — but it is
			// still one the caller should be able to see.
			if !errors.Is(err, fileproc.ErrUnsupportedEmbed) {
				out.fail("in-file write", err)
			}
		} else {
			out.InFileWritten = true
		}
	}

	if eff.Sidecar && w.deps.Sidecar != nil {
		mode := sidecar.ModeFull
		if out.InFileWritten {
			mode = sidecar.ModeSpillover
		}
		if err := w.writeSidecar(ctx, b, handle, mode); err != nil {
			// Nothing compensates for this one. When the in-file step
			// was skipped or failed, the sidecar is the only portable
			// copy of the edit (ADR-0001).
			out.fail("sidecar", err)
		} else {
			out.SidecarWritten = true
			out.SidecarMode = mode
		}
	}

	if eff.FolderRename && handle != nil {
		// Not recorded as a failure: renameFolder returns false both when
		// a rename genuinely broke and when it declined (nothing to move,
		// target already correct), and the two are not distinguishable at
		// this seam. It would warn on every edit of a legacy flat-layout
		// book. A failed rename also leaves the file intact, just at its
		// old path — unlike a lost sidecar, nothing is unrecoverable.
		// Both impls log the specific cause.
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
// Both arms move the bytes through Storage.MovePrefix; what differs is
// the policy wrapped around it, which is why the two arms still exist.
// Local: the adapter's move is atomic, there is nothing to reclaim, and
// the DB catch-up is best-effort (ADR-0003 §6.5). Backend (S3): the
// adapter copies and hands back the live sources, and this module wraps
// a single-tx DB swap plus sweeper-deferred deletes around them
// (ADR-0005).
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
//   - New layout (oldFolder != ""): the Book owns a folder. We move
//     the whole prefix through Storage.MovePrefix — one atomic
//     rename(2) inside the local adapter, carrying the sidecar and
//     any companion files for free.
func (w *MetadataWriter) renameFolderLocal(
	ctx context.Context,
	b model.Book,
	handle *LibraryHandle,
	oldFolder, newFolder string,
) (bool, string) {
	// libRoot deliberately reads Library.Path, not localRoot()'s
	// Root-then-Path preference. The two columns disagree and ADR-0030
	// files that as its own problem; switching which one the rename
	// reads under cover of this refactor would move books for reasons
	// nobody asked for.
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
		// happens to share Author+Title). Collision probing is policy
		// and stays here; only the move itself is the adapter's.
		oldAbs := filepath.Join(libRoot, oldFolder)
		finalAbs = uniqueDirectoryUnless(finalAbs, oldAbs)
		finalFolder = strings.TrimPrefix(finalAbs, libRoot+"/")

		if handle.Storage == nil {
			slog.Warn("metadata writer: rename folder skipped (no storage)", "book_id", b.ID)
			return false, ""
		}
		// Through the adapter, not os.Rename: a local library's LocalFS
		// is rooted at "/" (ADR-0030 §1), so these absolute paths are
		// exactly the keys it answers to. The MoveResult is empty by
		// contract for an atomic backend — nothing written to reclaim,
		// no source left to schedule — which is why this arm has no
		// rollback and the one below it does.
		if _, err := handle.Storage.MovePrefix(ctx, oldAbs, finalAbs); err != nil {
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
//  1. Resolve a non-colliding new prefix via uniqueBackendFolder.
//  2. Storage.MovePrefix the old folder onto it. The adapter owns the
//     list, the per-key copy and its retry budget; it hands back the
//     destinations it wrote and the sources it left alive.
//  3. On failure, schedule MoveResult.Written with RenameRollbackGrace
//     and bail.
//  4. Compute the per-files location updates for the rows DB knows
//     about (others ride along under the new prefix without a row).
//  5. RenameFolderTx: single transaction wraps files updates +
//     books folder_path/path + INSERT pending_orphans
//     (MoveResult.Reclaim, RenameGrace).
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

	moved, err := handle.Storage.MovePrefix(ctx, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename move prefix",
			"book_id", b.ID, "from", oldPrefix, "to", newPrefix, "err", err)
		// Written is populated even on error — a copy loop that broke
		// halfway still created objects, and this module owns the
		// decision to reclaim them.
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return false, ""
	}

	fileUpdates, err := w.collectFileLocationUpdates(ctx, b, oldPrefix, newPrefix)
	if err != nil {
		slog.Warn("metadata writer: backend rename file enumeration",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
		return false, ""
	}

	newBookPath := newPrefix + filepath.Base(b.Path)
	// Reclaim, not the source listing: the adapter says which sources
	// are still there. An atomic backend leaves none and enqueues none.
	orphanInserts := buildOrphanInserts(handle.Library.ID, moved.Reclaim, w.renameGrace(), b.ID)

	if err := w.deps.Books.RenameFolderTx(ctx, repo.RenameFolderTxArgs{
		BookID:    b.ID,
		NewFolder: finalFolder,
		NewPath:   newBookPath,
		Files:     fileUpdates,
		Orphans:   orphanInserts,
	}); err != nil {
		slog.Warn("metadata writer: backend rename db tx",
			"book_id", b.ID, "err", err)
		w.scheduleOrphans(ctx, handle.Library.ID, moved.Written, RenameRollbackGrace, b.ID)
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
func (w *MetadataWriter) embedAndStamp(ctx context.Context, b model.Book, handle *LibraryHandle) error {
	emb, err := w.deps.Dispatch(b.Format)
	if err != nil {
		// Formats with no in-file target reach here on every edit; the
		// sidecar carries the full mirror instead (ADR-0001 §3). Returned
		// so the caller can tell "nothing to write" from "the write
		// broke", which the previous bare false could not express.
		return err
	}
	// Through storageKey, for the same reason OpenBook goes through it: a
	// local install's LocalFS is rooted at "/", and books.path is
	// library-relative for everything placed since storage-v2. Handing it
	// over raw asked the filesystem for /Author/Title/book.epub, failed,
	// and logged a warning — so the in-file embed ADR-0001 promises has
	// been quietly off for every locally-approved book (#168).
	key := handle.StorageKey(b.Path)
	src, err := handle.Storage.Open(ctx, key)
	if err != nil {
		slog.Warn("metadata writer: open source", "book_id", b.ID, "key", key, "err", err)
		return err
	}
	defer func() { _ = src.Close() }()
	em := b.Editable()
	em.PublishedDate = dateString(b.PublishDate)
	in := fileproc.EmbedInput{EditableMetadata: em}
	out, err := emb.Embed(ctx, src, in)
	if err != nil {
		slog.Warn("metadata writer: embed", "book_id", b.ID, "format", b.Format, "err", err)
		return err
	}
	if _, err := handle.Storage.Put(ctx, key, bytes.NewReader(out)); err != nil {
		slog.Warn("metadata writer: put", "book_id", b.ID, "key", key, "err", err)
		return err
	}
	if w.deps.Files != nil {
		w.stampFileHash(ctx, b, out)
	}
	return nil
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
func (w *MetadataWriter) writeSidecar(ctx context.Context, b model.Book, handle *LibraryHandle, mode sidecar.WriteMode) error {
	// storageKey first: SidecarKey only swaps the filename, so a
	// library-relative books.path would have written the sidecar to the
	// filesystem root on a local install (#168).
	key := handle.SidecarKey(handle.StorageKey(b.Path))
	side := b.Editable()
	side.PublishedDate = dateString(b.PublishDate)
	if err := w.deps.Sidecar.Write(ctx, handle.Storage, key, side, mode, b.Format); err != nil {
		slog.Warn("metadata writer: sidecar write", "book_id", b.ID, "key", key, "err", err)
		return err
	}
	return nil
}

// dateString formats a *time.Time for the sidecar's PublishedDate
// string field. Returns "" when t is nil.
func dateString(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}
