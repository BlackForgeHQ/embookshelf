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
	SetFolderPath(ctx context.Context, bookID, folderPath, path string) error
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

// renameFolder executes ADR-0003 §6.5 step 5: move the on-disk Book
// folder to its new {Author}/{Title} location and update every
// files.location + books.folder_path + books.path to match. Local
// backends only — DecideEffects gates S3 out of this step.
//
// Two cases:
//   - Legacy flat-layout (oldFolder == ""): files sit at library
//     root. We move only this Book's files (per files.location),
//     preserving siblings that belong to other flat-layout Books.
//   - New layout (oldFolder != ""): the Book owns a folder. We
//     rename the directory wholesale via os.Rename — atomic on
//     same FS, carries the sidecar and any companion files for
//     free.
//
// On any failure the on-disk + DB state is left consistent with the
// pre-rename world (the DB metadata is already committed; the file
// is correct at the old path). The caller / scan can retry.
func (w *MetadataWriter) renameFolder(
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
