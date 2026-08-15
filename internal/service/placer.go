// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/layout"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Placer materializes a bookdrop file at its final location for a Library.
// Two adapters: LocalPlacer (filesystem move into the per-Book folder
// under the library root) and BackendPlacer (stream-upload to a Storage
// backend at the equivalent key prefix). Approve picks the adapter at
// runtime via PlacerBuilder, so the service never branches on
// "where do bytes go".
//
// Per ADR-0003, every Book lives at `{library_root}/{Author}/{Title}/`.
// PlaceSource carries Author and Title so the adapter can build that
// folder; empty values fall back to the layout package's sentinels.
//
// Contract: Place is destructive on the source path. After it returns,
// the caller MUST treat src.Path as gone. Adapters capture size/mtime
// before bytes move out of reach and surface them in PlaceResult so
// callers do not need to re-stat.
//
// Scope: this seam names the destination as well as writing it — the
// {Author}/{Title}/ folder, the collision suffix, the source's basename.
// That is only right for a book that does not exist yet. Writing into a
// book that already does is LibraryHandle.PlaceAt, which is handed the
// location and overwrites it; it is a different question, not a mode
// here, and PlaceAt is where the argument is written down.
type Placer interface {
	Place(ctx context.Context, src PlaceSource) (PlaceResult, error)
}

// PlaceSource is the inbound description of bytes to place. Format is
// the books.format value (used by adapters that need a Content-Type).
// Author and Title drive the folder layout; empty values fall back to
// `Unknown Author` / `Untitled` per ADR-0003 §2.
type PlaceSource struct {
	Path   string
	Format string
	Author string
	Title  string
}

// PlaceResult is what the files row needs after a successful Place.
// Location is library-relative (LocalPlacer strips the library root,
// BackendPlacer returns the backend key directly, LibraryHandle.PlaceAt
// returns the location it was given). FolderPath is the library-relative
// directory containing the file — what books.folder_path stores per
// ADR-0003.
type PlaceResult struct {
	Location   string
	FolderPath string
	Size       int64
	Mtime      time.Time
}

// PlacerBuilder constructs the right Placer for a Library. Injected
// into BookDropService at boot from main.go where the storage.Resolver
// is already available.
type PlacerBuilder func(model.Library) (Placer, error)

// DefaultPlacerBuilder picks the adapter by asking the resolved backend
// whether it is an object store. nil resolver is allowed — the local
// branch ignores it.
//
// It used to dispatch on Library.BackendID, and that column is not the
// question. It says a backend row exists, not that the library is
// something other than local — the same confusion #202 was about. The
// storage-v2 backfill seeds one kind=local backend row per distinct
// libraries.path and wires every pre-existing library to it
// (migrator.wireLibraries), and every local backend is constructed
// rooted at "/" for the whole instance (storageloader.buildBackend), so
// a migrated local library got BackendPlacer over a "/"-rooted LocalFS
// and its library-relative key put the book's file at the filesystem
// root while the files row recorded it as inside the library (#265).
//
// So the builder asks LibraryHandle.IsObjectStore, of the adapter and
// through the handle's own method rather than a second reading of the
// capability bit: this is the same question the keys seam asks on the walk,
// resolve and write paths, and one answer is the property those paths
// are named for.
func DefaultPlacerBuilder(resolver storage.Resolver) PlacerBuilder {
	return func(lib model.Library) (Placer, error) {
		if lib.BackendID != nil && resolver != nil {
			store, err := resolver.Resolve(*lib.BackendID)
			if err != nil {
				return nil, fmt.Errorf("resolve backend: %w", err)
			}
			// The capability bit directly — the same question
			// IsObjectStore answers, without building a throwaway handle
			// to ask it (#346).
			if store.Capabilities()&storage.CapObjectStore != 0 {
				return BackendPlacer{Store: store}, nil
			}
		}
		// Local, whether or not a backend row exists. libraryLocalRoot is
		// the one reading of where the library lives on disk — storage-v2's
		// root column, falling back to the legacy path.
		root := strings.TrimRight(libraryLocalRoot(lib), "/")
		if root == "" {
			return nil, ErrNoPlaceRoot
		}
		return LocalPlacer{Root: root}, nil
	}
}

// LocalPlacer moves bytes into the Book's folder under the library
// filesystem root and returns the library-relative location for the
// files row. The folder path is `{Author}/{Title}/` per ADR-0003;
// collisions on the title segment are resolved with a ` (2)`, ` (3)`
// suffix.
type LocalPlacer struct {
	Root string
}

func (p LocalPlacer) Place(_ context.Context, src PlaceSource) (PlaceResult, error) {
	// The Placer's reading of the root: whatever it was built with,
	// which DefaultPlacerBuilder takes from libraryLocalRoot. The
	// renamer reads Library.Path instead and the two stay apart
	// (ADR-0030 Consequences); libRoot does the arithmetic, not the
	// choosing.
	root := newLibRoot(p.Root)
	if root.empty() {
		return PlaceResult{}, errors.New("local placer: empty root")
	}
	st, err := os.Stat(src.Path)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("stat source: %w", err)
	}

	authorSeg := layout.SanitizeAuthor(src.Author)
	titleSeg := layout.SanitizeTitle(src.Title)

	if err := os.MkdirAll(root.abs(authorSeg), 0o755); err != nil {
		return PlaceResult{}, fmt.Errorf("mkdir author dir: %w", err)
	}

	// Both forms at once: the absolute one to create and move into, the
	// relative one for the files row. Nothing here trims a root back off
	// a path it just joined one onto.
	bookDir, folderPath, err := root.freeDir(path.Join(authorSeg, titleSeg))
	if err != nil {
		return PlaceResult{}, fmt.Errorf("book dir: %w", err)
	}
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		return PlaceResult{}, fmt.Errorf("mkdir book dir: %w", err)
	}

	dest := filepath.Join(bookDir, filepath.Base(src.Path))
	if dest != src.Path {
		dest = freeFilePath(dest)
		if err := moveFile(src.Path, dest); err != nil {
			return PlaceResult{}, fmt.Errorf("move: %w", err)
		}
	}

	location, err := root.rel(dest)
	if err != nil {
		// The bytes are at dest and the row is not written: better a
		// failed import the caller can retry than a files row holding an
		// absolute location, which is what the old fallback wrote.
		return PlaceResult{}, fmt.Errorf("location: %w", err)
	}

	return PlaceResult{
		Location:   location,
		FolderPath: folderPath,
		Size:       st.Size(),
		Mtime:      st.ModTime(),
	}, nil
}

// BackendPlacer streams the source file into a Storage at a key derived
// from `{Author}/{Title}/{basename}` (per ADR-0003 §7), then removes the
// local source. The backend itself encodes any per-library prefix
// (Plan F), so Place returns the bare key as the location.
type BackendPlacer struct {
	Store storage.Storage
}

func (p BackendPlacer) Place(ctx context.Context, src PlaceSource) (PlaceResult, error) {
	st, err := os.Stat(src.Path)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("stat source: %w", err)
	}
	f, err := os.Open(src.Path)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = f.Close() }()

	authorSeg := layout.SanitizeAuthor(src.Author)
	titleSeg := layout.SanitizeTitle(src.Title)
	// backendRoot, not a root of its own: the backend encodes any
	// per-library prefix (Plan F), so the folder this probe returns is
	// already both the key and the library-relative location.
	folderKey, err := backendRoot().freeDirBackend(ctx, p.Store, path.Join(authorSeg, titleSeg))
	if err != nil {
		return PlaceResult{}, fmt.Errorf("book folder: %w", err)
	}
	key := path.Join(folderKey, filepath.Base(src.Path))

	opts := []storage.PutOption{}
	if mime := storageMIMEForFormat(src.Format); mime != "" {
		opts = append(opts, storage.WithContentType(mime))
	}
	if _, err := p.Store.Put(ctx, key, f, opts...); err != nil {
		return PlaceResult{}, fmt.Errorf("upload: %w", err)
	}

	// Local source is now redundant. Failure is logged but non-fatal —
	// the bytes are durable in the backend; the leftover file will be
	// reaped by the bookdrop watcher (the row is already imported).
	if err := os.Remove(src.Path); err != nil {
		slog.Warn("placer: remove local after upload",
			"path", src.Path, "err", err)
	}
	return PlaceResult{
		Location:   key,
		FolderPath: folderKey,
		Size:       st.Size(),
		Mtime:      st.ModTime(),
	}, nil
}

// relativeToRoot is gone into libRoot.rel, with the renamer's hand-rolled
// prefix trim. Both fell through to returning the absolute path when the
// prefix did not match — ADR-0030's Consequences names the pair as the
// live producers of absolute locations — and there is now one of them,
// which answers with an error instead (#323).

// storageMIMEForFormat is the Content-Type a presigned URL should serve
// the bytes under. Used by BackendPlacer on Put.
//
// It used to be a hand-copy of the file handler's table, under a comment
// saying so. Both now read model.FormatSpecs, so "mirrors" is structural
// rather than aspirational (#194).
func storageMIMEForFormat(format string) string {
	return model.MIMEForFormat(format)
}
