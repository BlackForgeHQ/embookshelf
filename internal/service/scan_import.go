package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"time"

	"github.com/blackforge/embookshelf/internal/coverstore"
	"github.com/blackforge/embookshelf/internal/extractor"
	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/scan"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ScanImportLeafBook is the dependency surface ScanImport needs.
// Defined here so tests can fake it without standing up real
// repositories or storage.
type ScanImportLeafBookDeps struct {
	LibStore LibraryStoreFor
	Books    BookCreator
	Files    ScanFileRepo
	Covers   CoverPromoter
}

// BookCreator is the slice of *repo.BookRepo ScanImport needs to
// insert a fresh Book row. Mirrors the shape Approve uses, plus
// UpdateAudio for the audiobook fields that don't ride along with
// Create and SetFolderPath for content-hash reattach (ADR-0003 §10).
type BookCreator interface {
	Create(ctx context.Context, b model.Book) (model.Book, error)
	SetCoverHash(ctx context.Context, bookID string, hash []byte) error
	UpdateAudio(ctx context.Context, id string, durationSeconds *int, narrator string, chapters []model.Chapter) error
	SetFolderPath(ctx context.Context, bookID, folderPath, path string) error
}

// ScanFileRepo is the slice of *repo.FileRepo ScanImport needs to
// gate against duplicates, perform content-hash reattach (ADR-0003
// §10), and persist files rows.
type ScanFileRepo interface {
	ExistsByLocation(ctx context.Context, libraryID, location string) (bool, error)
	GetByContentHash(ctx context.Context, hash []byte) ([]model.File, error)
	Insert(ctx context.Context, f model.File) (model.File, error)
}

// CoverPromoter is the slice of *coverstore.Store ScanImport needs.
// SaveBookHashed writes raw cover bytes under the hash-keyed namespace
// used by the file-serve handler.
type CoverPromoter interface {
	SaveBookHashed(hash []byte, mime string, data []byte) error
}

// ScanImport materializes one classified scan.LeafBook into the
// canonical {books + files + cover + sidecar} state per ADR-0003 §3
// and ADR-0004 §1. It is the worker-level analog of
// BookDropService.Approve, minus the bookdrop staging step (the file
// is already in the library tree because the operator put it there).
//
// Inputs:
//   - libraryID: the Library this LeafBook belongs to.
//   - lb: classify output. lb.Folder is the library-relative folder
//     path; lb.Files holds every supported file in that folder.
//
// Behavior:
//  1. Resolves the library handle to get Storage + Library.Path.
//  2. Idempotency: if any file's location already exists in the
//     `files` table for this library, the LeafBook has already been
//     imported. Return ErrAlreadyImported so the caller can swallow.
//  3. Picks a primary file by format priority (EPUB > PDF > CBZ >
//     AZW3 > MOBI > FB2 > M4B > MP3). Falls back to the first file
//     when no priority entry matches.
//  4. Opens the primary file via the library's Storage and runs
//     extractor.Extract for metadata + cover + audio fields +
//     sidecar overlay.
//  5. Inserts a books row with FolderPath set to lb.Folder and Path
//     set to the primary file's library-relative location.
//  6. Inserts a files row per LeafBook file (location, format, size,
//     mtime).
//  7. If the primary carried embedded cover bytes, promotes them to
//     the cover store under sha256(coverBytes) and persists the
//     hash via Books.SetCoverHash. Failure here is logged
//     non-fatally — books.cover_hash is optional.
//
// ErrAlreadyImported is a sentinel for "we've seen this LeafBook,
// noop." Other errors are propagated; River's retry policy decides
// whether to back off or fail the job.
func ScanImport(
	ctx context.Context,
	deps ScanImportLeafBookDeps,
	libraryID string,
	lb scan.LeafBook,
) (model.Book, error) {
	if deps.LibStore == nil {
		return model.Book{}, errors.New("scan import: library store not configured")
	}
	if deps.Books == nil {
		return model.Book{}, errors.New("scan import: book repo not configured")
	}
	if deps.Files == nil {
		return model.Book{}, errors.New("scan import: files repo not configured")
	}
	if len(lb.Files) == 0 {
		return model.Book{}, errors.New("scan import: empty LeafBook")
	}

	handle, err := deps.LibStore.For(ctx, libraryID)
	if err != nil {
		return model.Book{}, fmt.Errorf("scan import: lib store: %w", err)
	}
	if handle == nil || handle.Storage == nil {
		return model.Book{}, errors.New("scan import: no storage on library handle")
	}

	for _, f := range lb.Files {
		exists, err := deps.Files.ExistsByLocation(ctx, libraryID, f.Location)
		if err != nil {
			return model.Book{}, fmt.Errorf("scan import: existence check: %w", err)
		}
		if exists {
			return model.Book{}, ErrAlreadyImported
		}
	}

	primary := pickPrimaryFile(lb.Files)
	primaryFormat := fileproc.FormatForExt(filepath.Ext(primary.Location))

	// Reattach (ADR-0003 §10): if the primary's content hash is
	// already in the files table for this library, this LeafBook is
	// the same Book that previously lived elsewhere on disk (the
	// operator moved it via `mv` outside our control). Fold into the
	// existing book_id, insert any new sibling files, update
	// folder_path. Skip the fresh-import path entirely.
	primaryHash, hashErr := hashViaStorage(ctx, handle.Storage, primary.Location)
	if hashErr != nil {
		slog.Warn("scan import: primary hash failed (skipping reattach)",
			"library_id", libraryID, "location", primary.Location, "err", hashErr)
	} else if matches, err := deps.Files.GetByContentHash(ctx, primaryHash); err == nil {
		for _, m := range matches {
			if m.LibraryID == libraryID && m.BookID != "" {
				if reattached, rErr := reattachLeafBook(ctx, deps, libraryID, lb, m, primary, primaryHash); rErr == nil {
					return reattached, nil
				} else {
					slog.Warn("scan import: reattach failed, falling through to fresh import",
						"book_id", m.BookID, "err", rErr)
				}
				break
			}
		}
	} else {
		slog.Warn("scan import: hash lookup failed (skipping reattach)",
			"library_id", libraryID, "err", err)
	}

	src, err := handle.Storage.Open(ctx, primary.Location)
	if err != nil {
		return model.Book{}, fmt.Errorf("scan import: open primary: %w", err)
	}
	defer func() { _ = src.Close() }()

	res, extractErr := extractor.Extract(ctx, handle.Storage, src, primaryFormat, primary.Location)
	if extractErr != nil {
		return model.Book{}, fmt.Errorf("scan import: extract: %w", extractErr)
	}

	folder := lb.Folder
	book := model.Book{
		LibraryID:   libraryID,
		Title:       fallback(res.Title, "Untitled"),
		Author:      res.Author,
		Format:      primaryFormat,
		Description: res.Description,
		Language:    res.Language,
		Path:        primary.Location,
		HasCover:    res.HasCover,
		CoverMime:   res.CoverMime,
		FolderPath:  &folder,
	}

	created, err := deps.Books.Create(ctx, book)
	if err != nil {
		return model.Book{}, fmt.Errorf("scan import: create book: %w", err)
	}

	for _, f := range lb.Files {
		row := model.File{
			LibraryID:   libraryID,
			BookID:      created.ID,
			Location:    f.Location,
			Size:        f.Size,
			Mtime:       f.Mtime,
			Format:      fileproc.FormatForExt(filepath.Ext(f.Location)),
			LastScanned: time.Now(),
		}
		if _, err := deps.Files.Insert(ctx, row); err != nil {
			slog.Warn("scan import: insert files row",
				"book_id", created.ID, "location", f.Location, "err", err)
		}
	}

	// Audio metadata: MP3 / M4B carry duration + optional narrator on
	// the extractor result. Persist via UpdateAudio so audiobook
	// libraries don't lose those fields on auto-import. Non-audio
	// primaries leave both fields zero so the call is a noop shape.
	if isAudioFormatScan(primaryFormat) {
		if err := deps.Books.UpdateAudio(ctx, created.ID, res.DurationSeconds, res.Narrator, nil); err != nil {
			slog.Warn("scan import: update audio",
				"book_id", created.ID, "err", err)
		}
	}

	if res.HasCover && len(res.CoverBytes) > 0 && deps.Covers != nil {
		sum := sha256.Sum256(res.CoverBytes)
		if err := deps.Covers.SaveBookHashed(sum[:], res.CoverMime, res.CoverBytes); err != nil {
			slog.Warn("scan import: save cover", "book_id", created.ID, "err", err)
		} else if err := deps.Books.SetCoverHash(ctx, created.ID, sum[:]); err != nil {
			slog.Warn("scan import: set cover hash", "book_id", created.ID, "err", err)
		}
	}

	return created, nil
}

// ErrAlreadyImported reports that a LeafBook's files are already
// present in the files table — the import is idempotent and the
// caller should treat the call as a noop.
var ErrAlreadyImported = errors.New("scan import: leaf book already imported")

// pickPrimaryFile selects the highest-priority file in a LeafBook by
// format. Mirrors the precedence used by `books.format` selection
// across the rest of the system. Falls back to the first file when
// none of them match the priority list (audio-only LeafBook, etc.).
func pickPrimaryFile(files []scan.WalkEntry) scan.WalkEntry {
	priority := []string{"EPUB", "PDF", "CBZ", "AZW3", "MOBI", "FB2", "M4B", "MP3"}
	rank := func(loc string) int {
		f := fileproc.FormatForExt(filepath.Ext(loc))
		for i, p := range priority {
			if p == f {
				return i
			}
		}
		return len(priority)
	}
	sorted := append([]scan.WalkEntry{}, files...)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := rank(sorted[i].Location), rank(sorted[j].Location)
		if ri != rj {
			return ri < rj
		}
		return sorted[i].Location < sorted[j].Location
	})
	return sorted[0]
}

// hashViaStorage opens the file at key under store and returns the
// sha256 digest of its contents. Used by ScanImport's reattach
// (ADR-0003 §10) before the fresh-import path.
func hashViaStorage(ctx context.Context, store storage.Storage, key string) ([]byte, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// reattachLeafBook merges a freshly-walked LeafBook into an existing
// Book identified by content_hash match. Inserts any new files
// rows (location-wise), updates books.folder_path + books.path so
// the DB reflects the post-move on-disk layout. Returns the existing
// Book row in stub form (callers don't need the full row).
func reattachLeafBook(
	ctx context.Context,
	deps ScanImportLeafBookDeps,
	libraryID string,
	lb scan.LeafBook,
	matched model.File,
	primary scan.WalkEntry,
	_ []byte,
) (model.Book, error) {
	if matched.BookID == "" {
		return model.Book{}, errors.New("reattach: matched files row has no book_id")
	}

	// Insert files rows for every LeafBook file not already in DB at
	// this location. Lookup is per-file via ExistsByLocation; cheap
	// because we already know one row matched the hash.
	for _, f := range lb.Files {
		exists, err := deps.Files.ExistsByLocation(ctx, libraryID, f.Location)
		if err != nil {
			return model.Book{}, fmt.Errorf("reattach: exists by location: %w", err)
		}
		if exists {
			continue
		}
		row := model.File{
			LibraryID:   libraryID,
			BookID:      matched.BookID,
			Location:    f.Location,
			Size:        f.Size,
			Mtime:       f.Mtime,
			Format:      fileproc.FormatForExt(filepath.Ext(f.Location)),
			LastScanned: time.Now(),
		}
		if _, err := deps.Files.Insert(ctx, row); err != nil {
			slog.Warn("reattach: insert sibling file",
				"book_id", matched.BookID, "location", f.Location, "err", err)
		}
	}

	if err := deps.Books.SetFolderPath(ctx, matched.BookID, lb.Folder, primary.Location); err != nil {
		return model.Book{}, fmt.Errorf("reattach: set folder path: %w", err)
	}

	folder := lb.Folder
	return model.Book{
		ID:         matched.BookID,
		LibraryID:  libraryID,
		FolderPath: &folder,
		Path:       primary.Location,
	}, nil
}

// readCoverBytes is exposed for tests that want to verify the cover
// promotion path without standing up a full coverstore. Production
// uses io.ReadAll directly.
var readCoverBytes = func(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// Compile-time check that *coverstore.Store satisfies CoverPromoter.
var _ CoverPromoter = (*coverstore.Store)(nil)

// isAudioFormatScan duplicates the check in task/bookdrop.go to avoid
// pulling task into service. Mirrors the audio-format slug list used
// across the system.
func isAudioFormatScan(f string) bool {
	switch f {
	case "MP3", "M4B":
		return true
	}
	return false
}

// _ silence unused warning if path package goes unused after future
// refactors; keep here so file compiles even when callers vanish.
var _ = path.Join
