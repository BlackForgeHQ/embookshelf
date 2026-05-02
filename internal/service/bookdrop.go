package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
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
}

func NewBookDropService(
	bdrop *repo.BookDropRepo,
	libs *repo.LibraryRepo,
	books *repo.BookRepo,
	_ *repo.AppSettingsRepo, // retained for signature compatibility; unused after naming-pattern removal
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

func (s *BookDropService) List(ctx context.Context) ([]model.BookDropItem, error) {
	return s.bdrop.List(ctx)
}

func (s *BookDropService) Get(ctx context.Context, id string) (model.BookDropItem, error) {
	return s.bdrop.GetByID(ctx, id)
}

func (s *BookDropService) Enqueue(ctx context.Context, path, format string, size int64) (model.BookDropItem, bool, error) {
	item, err := s.bdrop.Insert(ctx, path, format, size)
	if err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			return item, false, nil
		}
		return item, false, err
	}
	s.broadcast(item.ID)
	return item, true, nil
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
	title, author, description, language string,
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

	if err := s.bdrop.SetMetadata(ctx, id, title, author, description, language, hasCover, coverMime); err != nil {
		return err
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
		return model.Book{}, errors.New("approve: no placer for library")
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
	if isAudioFormat(created.Format) {
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
	return created, nil
}

// isAudioFormat reports whether a books.format value names an audio file
// the AudioProcessor can extract metadata from.
func isAudioFormat(f string) bool {
	switch f {
	case "MP3", "M4B":
		return true
	}
	return false
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
		s.hub.Broadcast(sse.Event{Name: "bookdrop.cleared", Data: "{}"})
	}
	return len(ids), nil
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
	payload, _ := json.Marshal(map[string]string{"id": id})
	s.hub.Broadcast(sse.Event{Name: "bookdrop.updated", Data: string(payload)})
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
