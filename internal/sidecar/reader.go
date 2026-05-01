package sidecar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path"

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

// Read locates sidecar files near the given book key and returns the
// merged result. Lookup order: Calibre `metadata.opf` (in the book's
// directory, read-only compat) overlaid by the paired
// `<basename>.embookshelf.json`. The JSON sidecar wins on field
// conflicts because it's the format embookshelf actively writes.
//
// A book key with no sidecars present returns Sidecar{}, nil. A
// malformed sidecar at any layer is logged via slog.Warn and
// treated as absent, so the next overlay layer (or the empty
// fallback) takes effect — corrupt user-edited JSON never breaks
// ingest.
func Read(ctx context.Context, store storage.Storage, bookKey string) (Sidecar, error) {
	var merged Sidecar

	// 1. Calibre OPF in the book's directory.
	dir, _ := path.Split(bookKey)
	opfKey := dir + "metadata.opf"
	if parsed, err := readAndParse(ctx, store, opfKey, parseOPFData); err != nil {
		return Sidecar{}, err
	} else if !parsed.IsZero() {
		merged = Merge(merged, parsed)
	}

	// 2. Paired native JSON sidecar.
	jsonKey := KeyFor(bookKey)
	if parsed, err := readAndParse(ctx, store, jsonKey, parseJSONData); err != nil {
		return Sidecar{}, err
	} else if !parsed.IsZero() {
		merged = Merge(merged, parsed)
	}

	return merged, nil
}

// readAndParse fetches a single sidecar object and parses via fn.
// Both ErrNotFound and parse errors return Sidecar{}, nil so the
// caller can fall through to the next overlay layer. Parse errors
// emit slog.Warn before swallowing.
func readAndParse(ctx context.Context, store storage.Storage, key string, fn func([]byte) (Sidecar, error)) (Sidecar, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return Sidecar{}, nil
		}
		return Sidecar{}, err
	}
	data, readErr := io.ReadAll(rc)
	_ = rc.Close()
	if readErr != nil {
		return Sidecar{}, readErr
	}
	parsed, parseErr := fn(data)
	if parseErr != nil {
		slog.Warn("sidecar: malformed payload, ignoring",
			"key", key, "err", parseErr)
		return Sidecar{}, nil
	}
	return parsed, nil
}

func parseOPFData(data []byte) (Sidecar, error)  { return ParseOPF(data) }
func parseJSONData(data []byte) (Sidecar, error) { return DecodeJSON(data) }
