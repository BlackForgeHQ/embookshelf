// Package hashing computes content hashes from a storage.Storage
// stream. sha256 is the project-wide identity primitive.
package hashing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/blackforge/embookshelf/internal/storage"
)

// HashFile streams bytes from store at key, returning the sha256
// digest and the byte count read. The underlying ReadCloser is
// closed before return. Cancellation is honored via ctx — if the
// caller cancels, io.Copy aborts and the partial hash is discarded.
func HashFile(ctx context.Context, store storage.Storage, key string) ([]byte, int64, error) {
	rc, err := store.Get(ctx, key)
	if err != nil {
		return nil, 0, fmt.Errorf("hashing: get %q: %w", key, err)
	}
	defer func() { _ = rc.Close() }()

	// Check for cancellation before starting the potentially-large read.
	if err := ctx.Err(); err != nil {
		return nil, 0, fmt.Errorf("hashing: read %q: %w", key, err)
	}

	h := sha256.New()
	n, err := io.Copy(h, rc)
	if err != nil {
		return nil, 0, fmt.Errorf("hashing: read %q: %w", key, err)
	}
	return h.Sum(nil), n, nil
}
