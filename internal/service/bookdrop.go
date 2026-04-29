package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/pattern"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/sse"
)

// BookDropService orchestrates the bookdrop ingest pipeline. It sits between
// handlers (which mutate state on user actions) and workers (which apply
// processor results). All state transitions go through here so SSE events,
// cover-file side effects, and the state machine stay in one place.
type BookDropService struct {
	bdrop    *repo.BookDropRepo
	libs     *repo.LibraryRepo
	settings *repo.AppSettingsRepo
	covers   *coverstore.Store
	hub      *sse.Hub
	// files is the storage_v2 file repo. When non-nil, Approve writes a
	// files row alongside the new book. nil disables the write so callers
	// (e.g. tests) that don't need the row don't have to supply a repo.
	files *repo.FileRepo
}

func NewBookDropService(
	bdrop *repo.BookDropRepo,
	libs *repo.LibraryRepo,
	settings *repo.AppSettingsRepo,
	covers *coverstore.Store,
	hub *sse.Hub,
	files *repo.FileRepo,
) *BookDropService {
	return &BookDropService{
		bdrop:    bdrop,
		libs:     libs,
		settings: settings,
		covers:   covers,
		hub:      hub,
		files:    files,
	}
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

	// File-naming pattern (spec/file-naming-patterns.spec.md §5.1). When
	// the target library has a pattern and at least one registered path,
	// resolve the template and physically move the file out of bookdrop
	// into the library root. Failures are logged but don't abort import —
	// the book still gets created pointing at the original bookdrop path.
	if newPath, ok := s.applyNamingPattern(ctx, libraryID, item); ok {
		book.Path = newPath
	}

	created, err := s.libs.Create(ctx, book)
	if err != nil {
		return created, err
	}

	// Persist the storage_v2 files row alongside the book. content_hash
	// was computed at ingest (Task 9); fall back to nil if it's missing,
	// the boot worker will fill it on next start.
	if s.files != nil {
		location := relativizeBookLocation(ctx, s.libs, libraryID, book.Path)
		size := int64(0)
		var mtime time.Time
		if st, statErr := os.Stat(book.Path); statErr == nil {
			size = st.Size()
			mtime = st.ModTime()
		} else {
			mtime = time.Now()
		}
		f := model.File{
			LibraryID:   libraryID,
			BookID:      created.ID,
			Location:    location,
			Size:        size,
			Mtime:       mtime,
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

	// Best-effort: hash the pre-approval cover and save it to the new
	// hash-keyed path. Failure is logged but doesn't abort the import —
	// the boot-time backfill will retry on the next start.
	if item.HasCover && s.covers != nil {
		func() {
			rc, err := s.covers.OpenBookDrop(item.ID)
			if err != nil {
				slog.Warn("approve cover: open bookdrop", "bookdrop_id", item.ID, "err", err)
				return
			}
			data, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				slog.Warn("approve cover: read", "bookdrop_id", item.ID, "err", err)
				return
			}
			sum := sha256.Sum256(data)
			if err := s.covers.SaveBookHashed(sum[:], item.CoverMime, data); err != nil {
				slog.Warn("approve cover: save hashed", "book_id", created.ID, "err", err)
				return
			}
			if err := s.libs.SetCoverHash(ctx, created.ID, sum[:]); err != nil {
				slog.Warn("approve cover: set hash", "book_id", created.ID, "err", err)
				return
			}
			// Best-effort cleanup of the bookdrop cover; non-fatal.
			if err := s.covers.DeleteBookDrop(item.ID); err != nil {
				slog.Warn("approve cover: delete bookdrop", "bookdrop_id", item.ID, "err", err)
			}
		}()
	}

	// Audiobook metadata: re-extract duration / narrator / chapters off
	// the file we just imported. The bookdrop schema doesn't carry audio
	// fields (the review surface only shows title/author/cover), so the
	// pragmatic option is one extra processor pass on approve. The cost
	// is bounded — for a typical M4B it's a single mvhd atom read; for
	// MP3 the XING header at the start of the file. Failure is logged
	// but never fatal: the book still imports without duration.
	if isAudioFormat(created.Format) && created.Path != "" {
		if meta, err := (fileproc.AudioProcessor{}).Extract(ctx, created.Path); err == nil {
			if err := s.libs.UpdateAudio(ctx, created.ID,
				meta.DurationSeconds, meta.Narrator, nil,
			); err != nil {
				slog.Warn("update audio metadata", "book_id", created.ID, "err", err)
			} else {
				if meta.DurationSeconds != nil {
					created.DurationSeconds = meta.DurationSeconds
				}
				created.Narrator = meta.Narrator
			}
		} else {
			slog.Warn("re-extract audio metadata", "book_id", created.ID, "err", err)
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

// applyNamingPattern places an approved bookdrop file under its
// target library's path. The library's FileNamingPattern (when set)
// decides the sub-path and filename; otherwise the original filename
// is reused. Returns (new absolute path, true) when the file was
// moved, or (false) when the file is already sitting where it should
// be and no move is needed.
//
// The move uses os.Rename where possible and falls back to copy+remove
// when source/destination straddle different filesystems (EXDEV).
// Destination collisions are resolved by appending " (2)", " (3)", etc.
func (s *BookDropService) applyNamingPattern(
	ctx context.Context,
	libraryID string,
	item model.BookDropItem,
) (string, bool) {
	lib, err := s.libs.GetByID(ctx, libraryID)
	if err != nil {
		slog.Warn("skip library placement: library lookup failed", "library_id", libraryID, "err", err)
		return "", false
	}
	root := strings.TrimRight(lib.Path, "/")
	if root == "" {
		slog.Warn("skip library placement: library has no path", "library_id", libraryID)
		return "", false
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Path)), ".")
	in := pattern.Input{
		Title:           item.Title,
		Authors:         splitAuthors(item.Author),
		Language:        item.Language,
		Extension:       ext,
		CurrentFilename: filepath.Base(item.Path),
	}
	// Pattern precedence: library override → instance default → no pattern.
	// Settings lookup is best-effort; a failure keeps the original filename
	// rather than wedging an approval on a transient DB blip.
	tmpl := ""
	if lib.FileNamingPattern != nil {
		tmpl = strings.TrimSpace(*lib.FileNamingPattern)
	}
	if tmpl == "" && s.settings != nil {
		if def, err := s.settings.GetDefaultNamingPattern(ctx); err != nil {
			slog.Warn("bookdrop: read default naming pattern", "err", err)
		} else {
			tmpl = strings.TrimSpace(def)
		}
	}
	var resolved string
	if tmpl != "" {
		resolved = pattern.Resolve(tmpl, in)
	}
	// Fallback to the original filename when no pattern is set or the
	// resolver returned empty — books still land under the library
	// root, just without a rename.
	if resolved == "" {
		resolved = in.CurrentFilename
	}

	// Forward-slash-only resolver output → OS-native path separator.
	dest := filepath.Join(root, filepath.FromSlash(resolved))
	if dest == item.Path {
		return "", false
	}
	dest = uniqueDestination(dest)
	if err := moveFile(item.Path, dest); err != nil {
		slog.Warn("bookdrop move failed, keeping original path",
			"src", item.Path, "dest", dest, "err", err)
		return "", false
	}
	return dest, true
}

// splitAuthors turns the bookdrop's single author string into a slice the
// resolver can reason about. Commas are the canonical separator since the
// extractor writes authors that way ("Name, Other Name"); a single name
// round-trips unchanged.
func splitAuthors(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// moveFile atomically moves src to dest. Falls back to copy+remove when
// os.Rename fails (cross-filesystem moves return syscall.EXDEV, but the
// spec's pragma is "try Rename, on any failure try the portable path").
// The destination directory is created as needed.
func moveFile(src, dest string) error {
	if src == dest {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	// Copy + sync + remove fallback. Intentionally not checking the
	// specific error type — any failure we can't rename through should
	// try the portable path before giving up.
	if err := copyFile(src, dest); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		// Leave the copy in place so the DB row still points to a valid
		// file; log so the admin can reap the source manually.
		slog.Warn("copy succeeded but source remove failed", "src", src, "err", err)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("fsync: %w", err)
	}
	return nil
}

// uniqueDestination walks " (2)", " (3)", … suffixes until it finds a free
// name. Preserves the original extension so the file still opens after
// renaming. Returns the input unchanged if it doesn't already exist.
func uniqueDestination(dest string) string {
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return dest
	}
	dir := filepath.Dir(dest)
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(filepath.Base(dest), ext)
	for i := 2; i < 10_000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return dest
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

// relativizeBookLocation strips the library root from abs, returning the
// path the files table stores. Falls back to abs on any lookup failure or
// when the path doesn't sit under the library root.
func relativizeBookLocation(ctx context.Context, libs *repo.LibraryRepo, libraryID, abs string) string {
	lib, err := libs.GetByID(ctx, libraryID)
	if err != nil {
		return abs
	}
	root := ""
	if lib.Root != nil {
		root = *lib.Root
	}
	if root == "" {
		root = lib.Path
	}
	if root == "" {
		return abs
	}
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.HasPrefix(abs, prefix) {
		return abs[len(prefix):]
	}
	return abs
}
