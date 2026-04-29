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
	Location string
	Size     int64
	Mtime    time.Time
	ETag     string
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
