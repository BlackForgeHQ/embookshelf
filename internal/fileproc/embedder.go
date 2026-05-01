package fileproc

import (
	"context"
	"errors"

	"github.com/blackforge/embookshelf/internal/storage"
)

// EmbedInput is the editable metadata payload an Embedder writes back
// into a book file. Mirrors the Sidecar struct's field set plus the
// cover bytes (which live outside Sidecar today because covers
// belong in coverstore, not in the JSON envelope). Empty fields are
// treated as "do not write" by some embedders; "explicitly clear"
// semantics are not supported in Phase 1 — empty edits in the UI
// either keep the previous value or write empty per format rules.
type EmbedInput struct {
	Title         string
	Subtitle      string
	Author        string
	Description   string
	Language      string
	Publisher     string
	PublishedDate string
	ISBN          string
	Series        string
	SeriesIndex   int
	Tags          []string
	Genres        []string

	// CoverBytes + CoverMime override the existing in-file cover when
	// non-nil. CoverBytes == nil means "leave the existing cover
	// alone." Empty CoverMime with non-nil bytes is invalid (the
	// caller must supply both).
	CoverBytes []byte
	CoverMime  string
}

// Embedder writes an EmbedInput snapshot back into the file's
// embedded metadata. One implementation per format. Embedders that
// don't support in-file write don't register here — DispatchEmbedder
// returns ErrUnsupportedEmbed for those formats so the caller can
// fall back to a sidecar-only write.
type Embedder interface {
	// Embed reads the existing file from src and returns the new
	// file bytes with in carried into the format's native metadata
	// slots. The caller is responsible for writing the returned
	// bytes back via storage.Put (atomic rename). src is consumed
	// fully; the caller should not Close it before Embed returns.
	Embed(ctx context.Context, src storage.Source, in EmbedInput) ([]byte, error)
}

// ErrUnsupportedEmbed is returned by DispatchEmbedder for formats
// without an in-file write implementation (CBZ, MOBI, AZW3, FB2,
// MP3/M4B in Phase 1).
var ErrUnsupportedEmbed = errors.New("fileproc: format does not support in-file embed")

// DispatchEmbedder picks the right embedder for a books.format value.
// Returns ErrUnsupportedEmbed for unsupported formats; the caller
// falls back to sidecar-only write in that case.
func DispatchEmbedder(format string) (Embedder, error) {
	switch format {
	case "EPUB":
		return EPUBEmbedder{}, nil
	}
	return nil, ErrUnsupportedEmbed
}

// EPUBEmbedder rewrites EPUB files. Real Embed implementation lands
// in epub_embed.go (Task 7); this stub satisfies the interface so
// DispatchEmbedder can return it and tests can type-assert.
type EPUBEmbedder struct{}

// Embed is a placeholder until Task 7. It returns an error so
// callers that wire it up early notice loudly rather than silently
// writing nothing.
func (EPUBEmbedder) Embed(ctx context.Context, src storage.Source, in EmbedInput) ([]byte, error) {
	return nil, errors.New("fileproc: EPUBEmbedder.Embed not implemented")
}
