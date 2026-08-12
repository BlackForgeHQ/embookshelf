// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
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
// The rename-side book writes (SetFolderPath, RenameFolderTx) belong
// to the FolderRenamer's RenameStore, not here.
type BookMetadataWriter interface {
	UpdateMetadata(ctx context.Context, b model.Book) error
}

// Renamer is the pipeline's view of the folder-rename module: one call
// with the Book, its handle and the folder delta; a RenameOutcome back.
// The migrate-vs-rename dispatch, collision probing and rollback policy
// are all the module's own business.
type Renamer interface {
	Relocate(ctx context.Context, b model.Book, handle *LibraryHandle, oldFolder, newFolder string) RenameOutcome
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
// Defined here so tests can fake it. The rename-side file writes
// (UpdateLocation) belong to the FolderRenamer's RenameFileStore.
type FileMetadataRepo interface {
	ListByBook(ctx context.Context, bookID string) ([]model.File, error)
	SetContentHash(ctx context.Context, fileID string, hash []byte, size int64, mtime time.Time) error
}

// MetadataWriterDeps groups the dependencies MetadataWriter needs.
// LibStore + Sidecar are nil-tolerant for the auto-enrichment-only
// case (DB write succeeds without them).
type MetadataWriterDeps struct {
	Books    BookMetadataWriter
	LibStore LibraryStore
	Sidecar  SidecarWriterFor
	Dispatch EmbedderDispatcher
	Files    FileMetadataRepo
	// Renamer is the folder-rename module. Nil declines every rename —
	// a degraded MetadataWriter still lands the DB, in-file and sidecar
	// steps.
	Renamer Renamer
}

// Outcome reports the post-execution facts of a Write call: what the
// pipeline did, not whether it managed to. What it failed to do travels
// on the error, as *Degraded. Tests pin behavior on these facts; SSE
// telemetry / audit may consume them later. Callers that don't need them
// can discard — discarding facts loses nothing a user is waiting for.
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
}

// StepFailure names a step that was planned and did not complete.
type StepFailure struct {
	// Step is the user-facing name of the step: "sidecar", "in-file
	// write", "book files", "cover art".
	Step string
	Err  error
}

// Past participles for how a Degraded call's steps did not complete.
// The only thing that ever differed between the write pipeline's
// degradation and the delete pipeline's.
const (
	degradeWritten = "written"
	degradeRemoved = "removed"
)

// Degraded is the result of a call whose authoritative step succeeded
// and whose best-effort tail did not. It is an error, and that is the
// whole point.
//
// The write and the delete pipeline both used to return this beside a
// nil error, so "the call succeeded" and "the copy on disk kept up" were
// the same value and twenty lines of prose asked callers to please
// consult the second return. They did not: the accessor that reported it
// had no production caller on either type, and the auto-enrich path
// dropped the outcome whole. As an error it cannot be dropped quietly.
// nil means every planned step landed. A *Degraded means the
// authoritative step landed and the named ones did not. Anything else
// means the call failed. A caller that checks err and does not
// distinguish the middle case reports a loud failure — a bug someone
// sees — instead of an unqualified success, which is a bug nobody sees.
//
// One type for both paths. The write pipeline's own facts
// (InFileWritten, SidecarMode, FolderRenamed, NewFolderPath) stay on
// Outcome, where they always belonged and where a delete has no reason
// to look; the degradation — named steps that did not happen, rendered
// for a human — was the same idea written twice, down to identical
// method bodies, with only the verb differing.
type Degraded struct {
	// Failures records the steps that were planned and did not complete:
	// the sidecar and in-file writes, whose silent loss costs an edit its
	// portable copy; the bytes and cover art, whose silent loss strands
	// them on disk. The authoritative step is never here — it fails the
	// call. Folder rename is not here either; see Write.
	Failures []StepFailure
	// verb is how those steps did not complete, for the human-facing
	// rendering: a write's step was "not written", a delete's "not
	// removed".
	verb string
}

// newDegraded starts an accumulator for a call's best-effort tail. It is
// built before the tail runs and is nil-as-an-error until something in
// the tail actually fails — see orNil.
func newDegraded(verb string) *Degraded { return &Degraded{verb: verb} }

// fail records a step that was planned and did not complete.
func (d *Degraded) fail(step string, err error) {
	d.Failures = append(d.Failures, StepFailure{Step: step, Err: err})
}

// orNil returns d as an error, or a nil error when the tail was clean.
// Every producer returns through this: handing back a *Degraded with no
// failures would be a non-nil error interface holding a nil-meaning
// value, the one way this design could lie.
func (d *Degraded) orNil() error {
	if d == nil || len(d.Failures) == 0 {
		return nil
	}
	return d
}

// Warnings renders the failures for a human. The response seam puts
// these on the body so the person who made the edit learns their change
// did not reach the file, instead of it only appearing in a server log.
func (d *Degraded) Warnings() []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d.Failures))
	for _, f := range d.Failures {
		out = append(out, fmt.Sprintf("%s not %s: %v", f.Step, d.verb, f.Err))
	}
	return out
}

// Error makes the degradation the call's error. The message is the same
// list a user would see, so a caller that only logs err still logs the
// steps that did not happen.
func (d *Degraded) Error() string {
	return "degraded: " + strings.Join(d.Warnings(), "; ")
}

// Degradation splits a call's error into the degradation it carries and
// whether it was fatal. The two questions are one lookup because
// answering only the first is how a fatal error gets read as success,
// and because every caller of a degradable call asks both.
//
//	deg, fatal := service.Degradation(err)
//	if fatal { ...the call did not happen... }
//	// past here the authoritative step landed; deg may be nil
func Degradation(err error) (deg *Degraded, fatal bool) {
	if err == nil {
		return nil, false
	}
	if errors.As(err, &deg) {
		return deg, false
	}
	return nil, true
}

// Effects is the plan returned by DecideEffects: the set of side
// effects the edit-side write pipeline will attempt for one Write
// call. Pure data; no I/O.
//
// The matrix encoded here is ADR-0001 §3 (trigger × backend) plus
// ADR-0003 §6 + ADR-0005 (folder rename on author/title edits, both
// backends). When adding a new trigger or backend, update this
// function and the table test in TestDecideEffects.
type Effects struct {
	// DB indicates the canonical books-row update will fire. Always
	// true for in-scope triggers; the field is explicit so callers
	// reading the plan don't infer.
	DB bool
	// Sidecar indicates the JSON sidecar write will fire. The mode
	// (full vs. spillover) is decided post-execution from Outcome.
	Sidecar bool
	// InFile indicates the in-file embedded write will be attempted.
	// Format-specific dispatch happens at execution time; an
	// unsupported format collapses to InFileWritten=false at runtime
	// without changing the plan.
	InFile bool
	// FolderRename indicates the on-disk folder (or S3 prefix) for this
	// Book will be moved to match the new sanitized {Author}/{Title}
	// per ADR-0003 §6 + ADR-0005. Set on user-driven triggers when the
	// sanitized folder name actually changed; backend-agnostic.
	FolderRename bool
}

// DecideEffects returns the side-effect plan for a Write call. The
// triggers in scope are the edit-side ones (manual_edit,
// apply_enrichment, auto_enrichment); approve and scan-reingest
// route around MetadataWriter and have their own (smaller) matrix.
//
// folderChanged is the caller-computed delta between the new
// {Author}/{Title} folder and the Book's stored folder_path. When
// false, no rename is scheduled even if the trigger and backend
// would otherwise allow it.
//
// Degraded handles (nil, or with no Storage) collapse to DB-only:
// without storage we cannot write a sidecar, open the source for
// in-file embed, or rename a folder. This mirrors the silent-skip
// behaviour the previous scattered nil-checks produced, but lifts
// it into one place.
func DecideEffects(trigger Trigger, handle *LibraryHandle, folderChanged bool) Effects {
	if trigger == TriggerAutoEnrichment {
		return Effects{DB: true}
	}
	if handle == nil || handle.Storage == nil {
		return Effects{DB: true}
	}
	e := Effects{DB: true, Sidecar: true}
	if folderChanged {
		// Both backends rename on user-driven Author/Title edits, and
		// both go through Storage.MovePrefix. Local: one atomic
		// rename(2). S3: copy + sweeper-deferred delete per ADR-0005.
		// Trigger gate is the same (TriggerAutoEnrichment
		// short-circuited above; only manual_edit + apply_enrichment
		// reach here).
		e.FolderRename = true
	}
	if !handle.IsObjectStore() {
		// In-file embed remains local-only — S3 still skips it per
		// ADR-0001. Sidecar full-mirror carries the metadata on S3.
		e.InFile = true
	}
	return e
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
// Only the DB step fails the call outright. The later steps are
// best-effort, but "best-effort" is not "unreported": when one of them
// does not land the error is a *Degraded naming it, so a nil error means
// the whole plan landed and nothing weaker. Callers split the two with
// Degradation; the response seam turns the degradation into warnings a
// user can act on, because someone whose edit did not reach the file
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
	deg := newDegraded(degradeWritten)

	if eff.InFile && w.deps.Dispatch != nil {
		if err := w.embedAndStamp(ctx, b, handle); err != nil {
			// ADR-0001 §3 treats a failed in-file write as a reason to
			// fall back to a full-mirror sidecar rather than an error,
			// so this is a degradation and not a failure — but it is
			// still one the caller should be able to see.
			if !errors.Is(err, fileproc.ErrUnsupportedEmbed) {
				deg.fail("in-file write", err)
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
			deg.fail("sidecar", err)
		} else {
			out.SidecarWritten = true
			out.SidecarMode = mode
		}
	}

	if eff.FolderRename && handle != nil && w.deps.Renamer != nil {
		// One call: whether this is a rename or the flat-layout
		// migration of ADR-0003 §5 is the renamer's dispatch, not the
		// pipeline's.
		res := w.deps.Renamer.Relocate(ctx, b, handle, oldFolder, newFolder)

		switch {
		case res.Done:
			out.FolderRenamed = true
			out.NewFolderPath = res.Folder
		case res.Declined != "":
			// Not a warning. Declining is the ordinary answer for a
			// degraded deployment or a Book with nothing to move, and
			// logging it as a failure meant every edit of such a book
			// looked broken.
			slog.Debug("metadata writer: folder rename declined",
				"book_id", b.ID, "reason", res.Declined)
		case res.Err != nil:
			// Still not a recorded StepFailure: a rename that broke
			// leaves the file intact at its old path, and unlike a lost
			// sidecar nothing is unrecoverable. What changed is that
			// this is now distinguishable from a decline, so it can be
			// logged as the failure it is — the specific cause is
			// already logged where it happened.
			slog.Warn("metadata writer: folder rename failed",
				"book_id", b.ID, "from", oldFolder, "to", newFolder, "err", res.Err)
		}
	}

	return out, deg.orNil()
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

	// No folder at all is the flat-layout book ADR-0003 §5 migrates
	// lazily, and the rename step is what performs that migration.
	if oldFolder == "" {
		return true, oldFolder, newFolder
	}

	// Not a plain inequality. A book whose folder took a collision
	// suffix at approve — "Author/Title (2)", because another book
	// sanitized to the same path — differs from the computed target on
	// every edit, so a plain comparison scheduled a rename for an edit
	// that changed only the description, walked the folder to (3), and
	// to (4) on the next save (#211).
	return !folderMatches(oldFolder, newFolder), oldFolder, newFolder
}

// collisionSuffix matches the " (2)", " (3)" … the placer appends when
// two books sanitize to the same folder (uniqueDirectory).
var collisionSuffix = regexp.MustCompile(`^(.*) \(\d+\)$`)

// folderMatches reports whether folder is where this author and title
// belong, allowing for the collision suffix.
//
// The suffix is part of the placer's naming, not part of the book's
// identity: two books can legitimately share an author and title, and
// the one that lost the race keeps its suffix for life. Reading it as a
// difference is what produced the churn.
func folderMatches(folder, ideal string) bool {
	if folder == ideal {
		return true
	}
	dir, base := filepath.Dir(folder), filepath.Base(folder)
	if m := collisionSuffix.FindStringSubmatch(base); m != nil {
		return filepath.Join(dir, m[1]) == ideal
	}
	return false
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
