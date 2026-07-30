// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scan orchestrates the two-phase library scan: walk, diff,
// then act on the changeset. The walker yields entries via a
// channel so the iterator API of storage.Storage doesn't leak into
// the differ.
package scan

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/blackforge/embookshelf/internal/storage"
)

// WalkEntry is one observation from a storage backend during the
// cheap walk phase. Hashes are NOT computed here.
type WalkEntry struct {
	// Location is what the files table stores: a location relative to
	// the library root (CONTEXT, "Files row"). Walk itself has no
	// library to be relative to, so it reports the listing key here and
	// leaves the rooting to whoever knows it —
	// service.LibraryHandle.Walk, which rewrites this field and is the
	// only walk the scan worker uses.
	Location string
	// Key is what the backend answers to for these bytes: the key it
	// listed the object under, carried through untouched. A caller that
	// needs to read an entry uses this rather than re-deriving a key
	// from Location, which on a "/"-rooted local backend means a round
	// trip back through the key shim on every single entry (ADR-0030 §2).
	Key   string
	Size  int64
	Mtime time.Time
	ETag  string
}

// Walk lists every object under root in store and forwards each as a
// WalkEntry. Errors during iteration go to errc; the caller MUST
// consume both channels to completion.
func Walk(ctx context.Context, store storage.Storage, root string) (<-chan WalkEntry, <-chan error) {
	out := make(chan WalkEntry, 64)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		it, err := store.List(ctx, root)
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = it.Close() }()
		for {
			obj, err := it.Next(ctx)
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				errc <- err
				return
			}
			entry := WalkEntry{
				Location: obj.Key,
				Key:      obj.Key,
				Size:     obj.Size,
				Mtime:    obj.ModTime,
				ETag:     obj.ETag,
			}
			select {
			case out <- entry:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
		}
	}()
	return out, errc
}
