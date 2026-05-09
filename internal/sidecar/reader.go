// SPDX-License-Identifier: AGPL-3.0-or-later

package sidecar

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path"

	"github.com/blackforge/embookshelf/internal/storage"
)

// KeyFor returns the canonical sidecar storage key for a book file's
// key. Per ADR-0003 §8 the sidecar lives at the LeafBook folder root
// as `metadata.embookshelf.json`, one file per Book regardless of how
// many file siblings share the folder.
//
// "Tolkien/Hobbit/hobbit.epub" → "Tolkien/Hobbit/metadata.embookshelf.json"
//
// Used by writers; readers also consult LegacyKeyFor for back-compat
// with pre-ADR-0003 sidecars next to each file.
func KeyFor(bookKey string) string {
	dir, _ := path.Split(bookKey)
	return dir + "metadata.embookshelf.json"
}

// LegacyKeyFor returns the pre-ADR-0003 paired sidecar key:
// "harry-potter.epub" → "harry-potter.embookshelf.json" (same dir).
// Used by Read as a fallback so existing libraries keep their
// sidecar overlays after upgrade. Never written — new writes always
// target KeyFor.
func LegacyKeyFor(bookKey string) string {
	dir, base := path.Split(bookKey)
	ext := path.Ext(base)
	stem := base
	if ext != "" {
		stem = base[:len(base)-len(ext)]
	}
	return dir + stem + ".embookshelf.json"
}

// Read locates sidecar files near the given book key and returns the
// merged result. Lookup order:
//  1. Calibre `metadata.opf` (in the book's directory, read-only compat).
//  2. Native JSON sidecar at the folder root (`metadata.embookshelf.json`,
//     ADR-0003 layout).
//  3. Legacy paired JSON sidecar (`<basename>.embookshelf.json`,
//     pre-ADR-0003). Read-only fallback.
//
// Each layer overlays the previous; the JSON sidecar wins on field
// conflicts because it's the format embookshelf actively writes.
// When both the new folder-root sidecar and the legacy paired sidecar
// exist, the folder-root file wins (it is the canonical location).
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

	// 2. Legacy paired JSON sidecar (older convention; read-only).
	if legacyKey := LegacyKeyFor(bookKey); legacyKey != "" {
		if parsed, err := readAndParse(ctx, store, legacyKey, parseJSONData); err != nil {
			return Sidecar{}, err
		} else if !parsed.IsZero() {
			merged = Merge(merged, parsed)
		}
	}

	// 3. Folder-root JSON sidecar (canonical post ADR-0003). Read
	//    last so it overlays/wins over the legacy paired sidecar
	//    when both exist.
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
