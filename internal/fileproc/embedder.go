// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// EmbedInput is the editable metadata payload an Embedder writes
// back into a book file. Composes model.EditableMetadata (the
// canonical editable scalar set) with the cover bytes — covers
// don't fit the Sidecar shape because they live in coverstore,
// not in the JSON envelope.
//
// CoverBytes + CoverMime override the existing in-file cover when
// non-nil. CoverBytes == nil means "leave the existing cover
// alone." Empty CoverMime with non-nil bytes is invalid (the
// caller must supply both).
type EmbedInput struct {
	model.EditableMetadata
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

// embedders is the embed axis's sibling of the processors map: the one
// fact the format table cannot hold without inverting the import — which
// format has an in-file writer. Which formats *declare* an in-file write
// target is the table's (FormatSpec.Embed); the parity test holds the
// two together (#335 — this used to be an off-table switch nothing
// checked).
var embedders = map[string]func() Embedder{
	"EPUB": func() Embedder { return EPUBEmbedder{} },
	"PDF":  func() Embedder { return PDFEmbedder{} },
}

// DispatchEmbedder picks the right embedder for a books.format value.
// Returns ErrUnsupportedEmbed for formats the table declares no in-file
// write target for; the caller falls back to sidecar-only write in that
// case (ADR-0001).
func DispatchEmbedder(format string) (Embedder, error) {
	s, ok := model.LookupFormat(format)
	if !ok || !s.Embed {
		return nil, ErrUnsupportedEmbed
	}
	e, ok := embedders[s.Format]
	if !ok {
		// A declared target with no writer is a wiring bug the parity
		// test catches at build time; refusing like an undeclared one
		// keeps the caller's sidecar fallback correct meanwhile.
		return nil, ErrUnsupportedEmbed
	}
	return e(), nil
}

// EPUBEmbedder rewrites EPUB files. Embed implementation lives in
// epub_embed.go.
type EPUBEmbedder struct{}
