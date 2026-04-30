package sidecar

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/blackforge/embookshelf/internal/storage"
)

// KeyFor returns the paired sidecar storage key for a book file's key.
// "harry-potter.epub" → "harry-potter.embookshelf.json" (same dir).
func KeyFor(bookKey string) string {
	dir, base := path.Split(bookKey)
	ext := path.Ext(base)
	stem := base
	if ext != "" {
		stem = base[:len(base)-len(ext)]
	}
	return dir + stem + ".embookshelf.json"
}

// SidecarFiles is the set of sibling filenames the reader looks for,
// in priority order. TOML wins (it's the native, app-edited format).
var SidecarFiles = []string{
	"metadata.opf",
	".embookshelf.toml",
}

// Read locates sidecar files under the given storage prefix
// (typically the directory containing a book). It parses each one
// it finds and merges them in priority order: OPF first, then TOML
// over it. A missing prefix or no sidecars present is not an error;
// the function returns Sidecar{}, nil.
func Read(ctx context.Context, store storage.Storage, prefix string) (Sidecar, error) {
	var merged Sidecar
	for _, name := range SidecarFiles {
		key := strings.TrimRight(prefix, "/") + "/" + name
		rc, err := store.Get(ctx, key)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return Sidecar{}, err
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			return Sidecar{}, readErr
		}

		var parsed Sidecar
		var parseErr error
		switch name {
		case "metadata.opf":
			parsed, parseErr = ParseOPF(data)
		case ".embookshelf.toml":
			parsed, parseErr = ParseTOML(data)
		}
		if parseErr != nil {
			// A malformed sidecar is logged via the caller; here we
			// surface the error so the scan worker can record it.
			return merged, parseErr
		}
		merged = Merge(merged, parsed)
	}
	return merged, nil
}
