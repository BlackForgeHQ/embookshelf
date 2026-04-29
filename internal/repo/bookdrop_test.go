package repo_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestBookDropRepo_contentHash verifies that SetContentHash persists the
// SHA-256 bytes on a bookdrop row and that they round-trip through the scan.
func TestBookDropRepo_contentHash(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			bdr := repo.NewBookDropRepo(d)
			ctx := t.Context()

			// 1. Insert a bookdrop item; content_hash should be nil.
			item, err := bdr.Insert(ctx, "/drop/book.epub", "EPUB", 1024)
			if err != nil {
				t.Fatalf("Insert: %v", err)
			}
			if item.ContentHash != nil {
				t.Fatalf("ContentHash should be nil on fresh insert, got %v", item.ContentHash)
			}

			// 2. SetContentHash writes the bytes.
			h := sha256.Sum256([]byte("file content"))
			hashBytes := h[:]
			if err := bdr.SetContentHash(ctx, item.ID, hashBytes); err != nil {
				t.Fatalf("SetContentHash: %v", err)
			}

			// 3. Read back and confirm round-trip.
			got, err := bdr.GetByID(ctx, item.ID)
			if err != nil {
				t.Fatalf("GetByID after SetContentHash: %v", err)
			}
			if !bytes.Equal(got.ContentHash, hashBytes) {
				t.Fatalf("ContentHash mismatch: got %x, want %x", got.ContentHash, hashBytes)
			}

			// 4. SetContentHash on a missing id → ErrNotFound.
			err = bdr.SetContentHash(ctx, "00000000-0000-0000-0000-000000000000", hashBytes)
			if !errors.Is(err, repo.ErrNotFound) {
				t.Fatalf("SetContentHash missing id: got %v, want ErrNotFound", err)
			}
		})
	}
}
