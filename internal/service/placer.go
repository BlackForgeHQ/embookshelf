package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Placer materializes a bookdrop file at its final location for a Library.
// Two adapters: LocalPlacer (filesystem move under the library root) and
// BackendPlacer (stream-upload to a Storage backend). Approve picks the
// adapter at runtime via PlacerBuilder, so the service never branches on
// "where do bytes go".
//
// Contract: Place is destructive on the source path. After it returns,
// the caller MUST treat src.Path as gone. Adapters capture size/mtime
// before bytes move out of reach and surface them in PlaceResult so
// callers do not need to re-stat.
type Placer interface {
	Place(ctx context.Context, src PlaceSource) (PlaceResult, error)
}

// PlaceSource is the inbound description of bytes to place. Format is
// the books.format value (used by adapters that need a Content-Type).
type PlaceSource struct {
	Path   string
	Format string
}

// PlaceResult is what the files row needs after a successful Place.
// Location is library-relative (LocalPlacer strips the library root,
// BackendPlacer returns the backend key directly).
type PlaceResult struct {
	Location string
	Size     int64
	Mtime    time.Time
}

// PlacerBuilder constructs the right Placer for a Library. Injected
// into BookDropService at boot from main.go where the storage.Resolver
// is already available.
type PlacerBuilder func(model.Library) (Placer, error)

// DefaultPlacerBuilder dispatches by Library.BackendID. nil resolver
// is allowed — the local branch ignores it.
func DefaultPlacerBuilder(resolver storage.Resolver) PlacerBuilder {
	return func(lib model.Library) (Placer, error) {
		if lib.BackendID != nil && resolver != nil {
			store, err := resolver.Resolve(*lib.BackendID)
			if err != nil {
				return nil, fmt.Errorf("resolve backend: %w", err)
			}
			return BackendPlacer{Store: store}, nil
		}
		root := strings.TrimRight(lib.Path, "/")
		if root == "" {
			return nil, errors.New("library has no path and no backend")
		}
		return LocalPlacer{Root: root}, nil
	}
}

// LocalPlacer moves bytes into a library's filesystem root and returns
// the library-relative location for the files row. Destination
// collisions are resolved by appending " (2)", " (3)", … to the basename
// before the extension.
type LocalPlacer struct {
	Root string
}

func (p LocalPlacer) Place(_ context.Context, src PlaceSource) (PlaceResult, error) {
	if p.Root == "" {
		return PlaceResult{}, errors.New("local placer: empty root")
	}
	st, err := os.Stat(src.Path)
	if err != nil {
		return PlaceResult{}, fmt.Errorf("stat source: %w", err)
	}
	dest := filepath.Join(p.Root, filepath.Base(src.Path))
	if dest != src.Path {
		dest = uniqueDestination(dest)
		if err := moveFile(src.Path, dest); err != nil {
			return PlaceResult{}, fmt.Errorf("move: %w", err)
		}
	}
	return PlaceResult{
		Location: relativeToRoot(dest, p.Root),
		Size:     st.Size(),
		Mtime:    st.ModTime(),
	}, nil
}

// BackendPlacer streams the source file into a Storage at a key derived
// from the original basename, then removes the local source. The backend
// itself encodes any per-library prefix (Plan F), so Place returns the
// bare key as the location.
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

	key := filepath.Base(src.Path)
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
		Location: key,
		Size:     st.Size(),
		Mtime:    st.ModTime(),
	}, nil
}

// relativeToRoot strips a library root prefix. Falls back to abs when
// the path doesn't sit under root (defensive — should not happen for
// LocalPlacer outputs).
func relativeToRoot(abs, root string) string {
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if strings.HasPrefix(abs, prefix) {
		return abs[len(prefix):]
	}
	return abs
}

// storageMIMEForFormat mirrors the Content-Type the file handler emits
// for downloads. Used by BackendPlacer on Put so presigned URLs serve
// with the correct content-type header.
func storageMIMEForFormat(format string) string {
	switch format {
	case "EPUB":
		return "application/epub+zip"
	case "PDF":
		return "application/pdf"
	case "CBZ":
		return "application/vnd.comicbook+zip"
	case "MP3":
		return "audio/mpeg"
	case "M4B":
		return "audio/mp4"
	}
	return ""
}

// moveFile atomically moves src to dest, falling back to copy+remove
// when os.Rename can't cross filesystems. The destination directory
// is created as needed.
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
	if err := copyFile(src, dest); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		// Leave the copy in place so the DB row still points to a
		// valid file; log so the admin can reap the source manually.
		slog.Warn("copy succeeded but source remove failed",
			"src", src, "err", err)
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

// uniqueDestination walks " (2)", " (3)", … suffixes until it finds a
// free name. Preserves the original extension. Returns the input
// unchanged if it doesn't already exist.
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
