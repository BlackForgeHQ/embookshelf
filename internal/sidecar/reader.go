package sidecar

import (
	"context"
	"errors"
	"io"
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
// A missing book or no sidecars present returns Sidecar{}, nil.
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
// ErrNotFound is treated as a non-error empty Sidecar.
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
		// Malformed sidecar: log via caller; return empty so the
		// caller can fall back to the next layer.
		return Sidecar{}, nil
	}
	return parsed, nil
}

func parseOPFData(data []byte) (Sidecar, error)  { return ParseOPF(data) }
func parseJSONData(data []byte) (Sidecar, error) { return DecodeJSON(data) }
