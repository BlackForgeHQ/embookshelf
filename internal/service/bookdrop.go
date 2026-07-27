// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sse"
)

// BookDropService orchestrates the bookdrop ingest pipeline. It sits between
// handlers (which mutate state on user actions) and workers (which apply
// processor results). All state transitions go through here so SSE events,
// cover-file side effects, and the state machine stay in one place.
type BookDropService struct {
	bdrop  *repo.BookDropRepo
	libs   *repo.LibraryRepo
	books  *repo.BookRepo
	covers *coverstore.Store
	hub    *sse.Hub
	// files is the storage_v2 file repo. When non-nil, Approve writes a
	// files row alongside the new book. nil disables the write so callers
	// (e.g. tests) that don't need the row don't have to supply a repo.
	files *repo.FileRepo
	// libStore is the deep seam Approve uses for everything library-aware:
	// fetching the Library row, building the Placer, opening Sources for
	// audio re-extract. nil disables Approve (it cannot place without it).
	libStore LibraryStore

	// bookdropPath is the staging directory the watcher polls and Wipe
	// targets. Empty disables Wipe (no path configured = nothing to wipe).
	bookdropPath string

	// wipeMu serialises Wipe against the intake paths so an upload or a
	// watcher tick fired mid-wipe can't insert a row pointing at bytes
	// about to vanish. Wipe takes the write-lock; Intake and Accept take
	// the read-lock for the whole write-and-insert sequence.
	wipeMu sync.RWMutex

	// intake overrides the insert seam used by Intake and Accept. nil
	// means "use bdrop"; tests set it to a fake so the intake sequence
	// runs against a temp dir with no database.
	intake bookdropInserter
	// dispatch hands a freshly-tracked item to the worker pool. nil means
	// no pool is wired — the row is still written.
	dispatch IngestDispatcher

	// enrichPolicy answers "is Auto-enrich enabled on this instance?" for
	// Approve. nil means no policy source, which reads as off.
	enrichPolicy autoEnrichPolicy
	// enrichDispatch hands a freshly-approved book to the worker pool for
	// Auto-enrich. nil means no pool is wired — the book is still imported.
	enrichDispatch EnrichDispatcher
}

// EnrichDispatcher hands a freshly-approved book to the worker pool for
// Auto-enrich. A function rather than a queue.Client because
// internal/queue imports this package; main.go supplies a closure over
// the river client. Same shape as IngestDispatcher and GuideDispatcher.
//
// nil is valid and means "no worker pool": the book is imported, it just
// never gets the background gap-fill.
type EnrichDispatcher func(ctx context.Context, bookID string) error

// autoEnrichPolicy is the slice of AppSettingsRepo the approve path
// reads — one flag, one method. Narrow so the decision is exercisable
// without the settings table.
type autoEnrichPolicy interface {
	GetBool(ctx context.Context, key string) (bool, error)
}

// WithAutoEnrich wires the Auto-enrich trigger Approve owns: the setting
// that enables it and the worker-pool handoff that carries it off the
// caller's goroutine. Both travel together because either one alone is
// inert — a policy with nowhere to dispatch, or a dispatcher no policy
// ever authorises.
func (s *BookDropService) WithAutoEnrich(p autoEnrichPolicy, d EnrichDispatcher) *BookDropService {
	s.enrichPolicy = p
	s.enrichDispatch = d
	return s
}

func NewBookDropService(
	bdrop *repo.BookDropRepo,
	libs *repo.LibraryRepo,
	books *repo.BookRepo,
	covers *coverstore.Store,
	hub *sse.Hub,
	files *repo.FileRepo,
) *BookDropService {
	return &BookDropService{
		bdrop:  bdrop,
		libs:   libs,
		books:  books,
		covers: covers,
		hub:    hub,
		files:  files,
	}
}

// WithLibraryStore wires the LibraryStore used by Approve. main.go
// builds the default (NewLibraryStore); tests can inject a fake to
// capture placement decisions and audio re-extract without touching
// disk, the resolver, or a backend.
func (s *BookDropService) WithLibraryStore(ls LibraryStore) *BookDropService {
	s.libStore = ls
	return s
}

// WithBookDropPath wires the staging directory used by Wipe. main.go
// passes cfg.BookDropPath at boot.
func (s *BookDropService) WithBookDropPath(path string) *BookDropService {
	s.bookdropPath = path
	return s
}

func (s *BookDropService) List(ctx context.Context) ([]model.BookDropItem, error) {
	return s.bdrop.List(ctx)
}

func (s *BookDropService) Get(ctx context.Context, id string) (model.BookDropItem, error) {
	return s.bdrop.GetByID(ctx, id)
}

func (s *BookDropService) BeginProcessing(ctx context.Context, id string) error {
	if err := s.bdrop.SetState(ctx, id, model.BookDropProcessing, 10, ""); err != nil {
		return err
	}
	s.broadcast(id)
	return nil
}

// RecordMetadata applies the worker's extracted metadata + cover bytes, then
// transitions to 'ready'. Cover write failures are logged but non-fatal — we
// still record the metadata and let the queue UI fall back to no-thumbnail.
func (s *BookDropService) RecordMetadata(
	ctx context.Context,
	id string,
	title, author, description, language, isbn string,
	coverBytes []byte,
	coverMime string,
) error {
	hasCover := len(coverBytes) > 0
	if hasCover && s.covers != nil {
		if err := s.covers.SaveBookDrop(id, coverBytes); err != nil {
			slog.Warn("save bookdrop cover", "id", id, "err", err)
			hasCover = false
			coverMime = ""
		}
	} else if !hasCover {
		coverMime = ""
	}

	if err := s.bdrop.SetMetadata(ctx, id, title, author, description, language, isbn, hasCover, coverMime); err != nil {
		return err
	}
	s.broadcast(id)
	return nil
}

// PutPreapprovalCover writes raw cover bytes for a BookDrop item that
// doesn't yet carry a cover. Used by the BookDrop preview UI to push
// a client-rendered PDF page-1 raster (see ADR-0015). Caller must
// ensure item.HasCover is false; this method does not re-check.
func (s *BookDropService) PutPreapprovalCover(ctx context.Context, id string, raw []byte, mime string) error {
	if s.covers == nil {
		return errors.New("cover store not configured")
	}
	if err := s.covers.SaveBookDrop(id, raw); err != nil {
		return fmt.Errorf("save cover bytes: %w", err)
	}
	if err := s.bdrop.SetCoverPresence(ctx, id, true, mime); err != nil {
		return fmt.Errorf("mark has_cover: %w", err)
	}
	s.broadcast(id)
	return nil
}

// SetAudio persists the audiobook fields extracted by the ingest worker.
// Used after RecordMetadata for MP3/M4B; non-audio formats skip this call.
// Failures bubble up to the worker which retries the whole job.
func (s *BookDropService) SetAudio(
	ctx context.Context,
	id string,
	durationSeconds *int,
	narrator string,
	chapters []model.Chapter,
) error {
	return s.bdrop.SetAudio(ctx, id, durationSeconds, narrator, chapters)
}

// SetContentHash persists the sha256 computed by the ingest worker.
// Plan B Task 9 / 11.
func (s *BookDropService) SetContentHash(ctx context.Context, id string, hash []byte) error {
	return s.bdrop.SetContentHash(ctx, id, hash)
}

func (s *BookDropService) Fail(ctx context.Context, id string, err error) error {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if e := s.bdrop.SetState(ctx, id, model.BookDropFailed, 0, msg); e != nil {
		return e
	}
	s.broadcast(id)
	return nil
}

// Approve creates a books row from the extracted metadata and flips the item
// into 'imported'. If the bookdrop item has a cover on disk, promote it into
// the book namespace before returning.
func (s *BookDropService) Approve(ctx context.Context, id, libraryID string) (model.Book, error) {
	item, err := s.bdrop.GetByID(ctx, id)
	if err != nil {
		return model.Book{}, err
	}
	if item.State != model.BookDropReady && item.State != model.BookDropFailed {
		return model.Book{}, fmt.Errorf("cannot approve item in state %q", item.State)
	}

	if libraryID == "" {
		libs, err := s.libs.List(ctx)
		if err != nil {
			return model.Book{}, err
		}
		if len(libs) == 0 {
			return model.Book{}, fmt.Errorf("no library configured — create one before approving imports")
		}
		libraryID = libs[0].ID
	}

	// Library access goes through LibraryStore. Approve never branches
	// on local-vs-S3 itself; the LibraryHandle's Placer is whichever
	// adapter was wired at construction (LocalPlacer or BackendPlacer).
	if s.libStore == nil {
		return model.Book{}, errors.New("approve: library store not configured")
	}
	handle, hErr := s.libStore.For(ctx, libraryID)
	if hErr != nil {
		return model.Book{}, fmt.Errorf("approve: library lookup: %w", hErr)
	}
	if handle.Placer == nil {
		if handle.PlacerErr != nil {
			return model.Book{}, fmt.Errorf("approve: no placer for library %s: %w", libraryID, handle.PlacerErr)
		}
		return model.Book{}, fmt.Errorf("approve: no placer for library %s", libraryID)
	}

	book := model.Book{
		LibraryID:   libraryID,
		Title:       fallback(item.Title, "Untitled"),
		Author:      item.Author,
		Format:      item.Format,
		Description: item.Description,
		Path:        item.Path,
		HasCover:    item.HasCover,
		CoverMime:   item.CoverMime,
	}
	// Route the extractor-supplied ISBN by length: book.ISBN is the
	// ISBN-13 slot, book.ISBN10 is the 10-digit slot. Mirrors the
	// length-based routing in enrichment.go so a Calibre PDF whose XMP
	// only carries an ISBN-10 doesn't pollute the ISBN-13 column.
	switch len(strings.TrimSpace(item.ISBN)) {
	case 13:
		book.ISBN = strings.TrimSpace(item.ISBN)
	case 10:
		book.ISBN10 = strings.TrimSpace(item.ISBN)
	}

	res, perr := handle.Placer.Place(ctx, PlaceSource{
		Path:   item.Path,
		Format: item.Format,
		Author: book.Author,
		Title:  book.Title,
	})
	if perr != nil {
		return model.Book{}, fmt.Errorf("approve: place: %w", perr)
	}
	book.Path = res.Location
	if res.FolderPath != "" {
		fp := res.FolderPath
		book.FolderPath = &fp
	}
	fileLocation := res.Location
	fileSize := res.Size
	fileMtime := res.Mtime

	created, err := s.books.Create(ctx, book)
	if err != nil {
		return created, err
	}

	// Persist the storage_v2 files row alongside the book. content_hash
	// was computed at ingest (Task 9); fall back to nil if it's missing,
	// the boot worker will fill it on next start.
	if s.files != nil && fileLocation != "" {
		f := model.File{
			LibraryID:   libraryID,
			BookID:      created.ID,
			Location:    fileLocation,
			Size:        fileSize,
			Mtime:       fileMtime,
			ContentHash: item.ContentHash,
			Format:      created.Format,
			LastScanned: time.Now(),
		}
		if _, err := s.files.Insert(ctx, f); err != nil {
			// Duplicate location is benign — a re-import of an existing
			// path. Other errors are logged but don't fail the approve.
			if !errors.Is(err, repo.ErrFileLocationTaken) {
				slog.Warn("approve: insert files row", "book_id", created.ID, "err", err)
			}
		}
	}

	if item.HasCover {
		s.promoteBookDropCover(ctx, item, created.ID)
	}

	// Audio metadata is captured at ingest now (bookdrop_items.duration_seconds,
	// .narrator, .chapters). Approve just copies the bookdrop fields onto the
	// books row — no Source open, no second processor pass. Failure is logged
	// but never fatal.
	if fileproc.IsAudioFormat(created.Format) {
		if item.DurationSeconds != nil || item.Narrator != "" || len(item.Chapters) > 0 {
			if err := s.books.UpdateAudio(ctx, created.ID,
				item.DurationSeconds, item.Narrator, item.Chapters,
			); err != nil {
				slog.Warn("update audio metadata", "book_id", created.ID, "err", err)
			} else {
				created.DurationSeconds = item.DurationSeconds
				created.Narrator = item.Narrator
				created.Chapters = item.Chapters
			}
		}
	}

	if err := s.bdrop.MarkImported(ctx, item.ID, created.ID); err != nil {
		return created, err
	}
	s.broadcast(item.ID)
	s.requestAutoEnrich(ctx, created.ID)
	return created, nil
}

// requestAutoEnrich asks the worker pool to gap-fill the new book's
// metadata from external providers, subject to the instance setting
// (ADR-0012).
//
// The decision lives inside Approve because it is part of what approving
// a book means, not part of what one caller happens to do afterwards.
// It used to sit in the HTTP handler, so the queue, the CLI and any bulk
// import silently got no enrichment at all and could not see the policy
// that governed it.
//
// Dispatch rather than call: Auto-enrich is a provider fan-out or an
// ISBN chain — seconds of upstream I/O — and running it here would hold
// the caller for the whole of it. Queued, it also survives a restart and
// retries a provider that was briefly down, neither of which an inline
// call could do.
//
// Every failure is logged and swallowed. The books row is committed by
// the time this runs; losing the gap-fill must not lose the import.
func (s *BookDropService) requestAutoEnrich(ctx context.Context, bookID string) {
	if s.enrichPolicy == nil || s.enrichDispatch == nil {
		return
	}
	on, err := s.enrichPolicy.GetBool(ctx, repo.SettingMetadataAutoEnrich)
	if err != nil {
		// Degrade closed, as the provider fan-out does: enriching a book
		// whose owner may have opted out is worse than not enriching one.
		slog.Warn("auto-enrich policy read", "book", bookID, "err", err)
		return
	}
	if !on {
		return
	}
	if err := s.enrichDispatch(ctx, bookID); err != nil {
		slog.Warn("dispatch auto-enrich", "book", bookID, "err", err)
	}
}

// promoteBookDropCover hashes the staged cover, moves it under the
// hash-keyed namespace, and writes the digest to books.cover_hash.
// Best-effort by contract: every failure is logged and swallowed so
// the import succeeds even when the cover side-effects don't. The
// boot-time covers backfill picks up books still missing a hash.
//
// Must be called only when item.HasCover is true and s.covers is wired.
// (Approve guards both before calling.)
func (s *BookDropService) promoteBookDropCover(ctx context.Context, item model.BookDropItem, bookID string) {
	if s.covers == nil {
		return
	}
	hash, err := s.covers.PromoteBookDrop(item.ID, item.CoverMime)
	if err != nil {
		slog.Warn("approve cover: promote", "bookdrop_id", item.ID, "book_id", bookID, "err", err)
		return
	}
	if err := s.books.SetCoverHash(ctx, bookID, hash); err != nil {
		slog.Warn("approve cover: set hash", "book_id", bookID, "err", err)
	}
}

// ClearProcessed drops every bookdrop row in a terminal state from the
// DB, then best-effort sweeps any pre-approval cover bytes still on disk
// (Reject already deletes them on the reject path, and Approve promotes
// them into the book namespace, so this is mostly a belt-and-suspenders
// cleanup for rows where one of those side effects failed).
//
// bookdrop.cleared is broadcast once after the batch — the queue UI
// refetches a single list, no need for per-row events.
//
// Note: the source files under BOOKDROP_PATH are NOT touched. An
// imported row's path is already referenced by a books row, and a
// rejected file that the user wants physically removed has to leave
// through the filesystem. The bookdrop watcher will re-discover any
// file that's still on disk with a deleted row on its next tick.
func (s *BookDropService) ClearProcessed(ctx context.Context) (int, error) {
	ids, err := s.bdrop.DeleteProcessed(ctx)
	if err != nil {
		return 0, err
	}
	if s.covers != nil {
		for _, id := range ids {
			if err := s.covers.DeleteBookDrop(id); err != nil {
				slog.Warn("clear processed cover", "id", id, "err", err)
			}
		}
	}
	if len(ids) > 0 && s.hub != nil {
		_ = s.hub.Publish(sse.BookDropCleared{})
	}
	return len(ids), nil
}

// BookDropFilesPreview is the staging-directory snapshot the wipe
// dialog renders before the user confirms.
type BookDropFilesPreview struct {
	// Count is the number of regular files under BOOKDROP_PATH that
	// would be deleted (i.e. excluding files referenced by 'processing'
	// rows).
	Count int `json:"count"`
	// Bytes is the total size in bytes of the files that would be deleted.
	Bytes int64 `json:"bytes"`
	// SkippedInFlight is the number of files left alone because a row
	// in 'processing' state currently references them.
	SkippedInFlight int `json:"skippedInFlight"`
}

// PreviewFiles walks BOOKDROP_PATH and returns the count + bytes that
// Wipe would remove, plus the count of in-flight files it would skip.
// The walk holds the wipe RLock so it stays consistent with the watcher.
func (s *BookDropService) PreviewFiles(ctx context.Context) (BookDropFilesPreview, error) {
	out := BookDropFilesPreview{}
	if s.bookdropPath == "" {
		return out, nil
	}
	skip, err := s.processingPathSet(ctx)
	if err != nil {
		return out, err
	}
	s.wipeMu.RLock()
	defer s.wipeMu.RUnlock()
	walkErr := filepath.WalkDir(s.bookdropPath, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if pathInSet(path, skip) {
			out.SkippedInFlight++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		out.Count++
		out.Bytes += info.Size()
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		return out, walkErr
	}
	return out, nil
}

func pathInSet(path string, set map[string]struct{}) bool {
	if _, ok := set[path]; ok {
		return true
	}
	if abs, err := filepath.Abs(path); err == nil {
		if _, ok := set[abs]; ok {
			return true
		}
	}
	return false
}

// BookDropWipeResult reports what Wipe actually did.
type BookDropWipeResult struct {
	Deleted         int   `json:"deleted"`
	Freed           int64 `json:"freed"`
	SkippedInFlight int   `json:"skippedInFlight"`
	OrphanRows      int   `json:"orphanRows"`
}

// WipeFiles recursively removes every regular file under BOOKDROP_PATH,
// skipping any file whose path is referenced by a 'processing' bookdrop
// row. After the file sweep it drops every non-'processing' bookdrop row
// whose path no longer exists on disk and broadcasts bookdrop.cleared.
//
// Empty subdirectories left behind are removed too — the staging area is
// expected to be (mostly) flat. The root BOOKDROP_PATH itself is never
// removed.
//
// Wipe holds the write-lock for the duration so the watcher's enqueue
// path can't race a fresh 'processing' row against a delete.
func (s *BookDropService) WipeFiles(ctx context.Context) (BookDropWipeResult, error) {
	res := BookDropWipeResult{}
	if s.bookdropPath == "" {
		return res, errors.New("bookdrop path not configured")
	}

	s.wipeMu.Lock()
	defer s.wipeMu.Unlock()

	skip, err := s.processingPathSet(ctx)
	if err != nil {
		return res, err
	}

	root := s.bookdropPath

	// First pass: delete files. In-flight files (those referenced by a
	// 'processing' row) and unreadable files are left alone — the DB
	// sweep below relies on os.Stat to find genuine orphans, so the
	// survival decision is made implicitly by what's still on disk.
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if pathInSet(path, skip) {
			res.SkippedInFlight++
			return nil
		}
		info, ierr := d.Info()
		var size int64
		if ierr == nil {
			size = info.Size()
		}
		if rmErr := os.Remove(path); rmErr != nil {
			slog.Warn("wipe bookdrop file", "path", path, "err", rmErr)
			return nil
		}
		res.Deleted++
		res.Freed += size
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, fs.ErrNotExist) {
		return res, walkErr
	}

	// Second pass: remove now-empty subdirectories under root. Skip the
	// root itself. Walks bottom-up.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || !d.IsDir() || path == root {
			return nil
		}
		// os.Remove on a dir succeeds only if empty.
		if rmErr := os.Remove(path); rmErr != nil {
			// Best-effort — if it has surviving in-flight files inside,
			// it stays.
			_ = rmErr
		}
		return nil
	})

	// DB sweep: drop every non-'processing' row whose path no longer
	// exists on disk. Stat'ing each path is robust against path-format
	// mismatches between the watcher's relative paths and the wipe's
	// absolute walk results — survived membership alone wasn't enough.
	rows, lerr := s.bdrop.ListNonProcessing(ctx)
	if lerr != nil {
		return res, lerr
	}
	for _, row := range rows {
		if _, statErr := os.Stat(row.Path); statErr == nil {
			// File still there (e.g. surviving in-flight or unreadable
			// during walk) — leave the row alone.
			continue
		}
		if derr := s.bdrop.DeleteByID(ctx, row.ID); derr != nil {
			slog.Warn("wipe bookdrop row", "id", row.ID, "err", derr)
			continue
		}
		res.OrphanRows++
		if s.covers != nil {
			if err := s.covers.DeleteBookDrop(row.ID); err != nil {
				slog.Warn("wipe bookdrop cover", "id", row.ID, "err", err)
			}
		}
	}

	if s.hub != nil {
		_ = s.hub.Publish(sse.BookDropCleared{})
	}

	return res, nil
}

func (s *BookDropService) processingPathSet(ctx context.Context) (map[string]struct{}, error) {
	paths, err := s.bdrop.ProcessingPaths(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		// Normalise to absolute so set membership matches the absolute
		// paths returned by filepath.WalkDir.
		if abs, aerr := filepath.Abs(p); aerr == nil {
			out[abs] = struct{}{}
			continue
		}
		out[p] = struct{}{}
	}
	return out, nil
}

func (s *BookDropService) Reject(ctx context.Context, id string) error {
	if err := s.bdrop.SetState(ctx, id, model.BookDropRejected, 100, ""); err != nil {
		return err
	}
	// Clean up the pre-approval cover so a rejected file doesn't squat on disk.
	if s.covers != nil {
		if err := s.covers.DeleteBookDrop(id); err != nil {
			slog.Warn("delete bookdrop cover", "id", id, "err", err)
		}
	}
	s.broadcast(id)
	return nil
}

func (s *BookDropService) broadcast(id string) {
	if s.hub == nil {
		return
	}
	_ = s.hub.Publish(sse.BookDropUpdated{ID: id})
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
