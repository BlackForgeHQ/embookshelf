// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ExtractResult is the unified output of one extraction pass over a book
// file: format-specific metadata from the embedded Processor, the cover
// bytes (if any), audio-only fields, and the merged Sidecar overlay.
//
// Sole consumer is the BookDrop ingest path — under ADR-0018, Library
// scan never extracts.
type ExtractResult struct {
	Format      string
	Title       string
	Author      string
	Description string
	Language    string
	ISBN        string
	HasCover    bool
	CoverBytes  []byte
	CoverMime   string
	// DurationSeconds is non-nil only for audio formats with a readable
	// header.
	DurationSeconds *int
	Narrator        string
}

// DispatchFormat returns the Processor for a books.format slug.
//
// The slug-keyed twin of Dispatch, which keys off a file extension. Both
// live here because format dispatch is this package's job; the slug entry
// point used to sit one package away and synthesize a fake filename
// ("x.epub") to feed the extension dispatcher — a module converting its
// own input backwards to satisfy the interface it wrapped.
func DispatchFormat(format string) (Processor, error) {
	switch strings.ToUpper(strings.TrimSpace(format)) {
	case "EPUB":
		return &EPUBProcessor{}, nil
	case "PDF":
		return &PDFProcessor{}, nil
	case "CBZ":
		return &CBZProcessor{}, nil
	case "MP3", "M4B":
		return &AudioProcessor{}, nil
	}
	return nil, ErrUnsupportedFormat
}

// ExtractBook runs the format-specific Processor against an open Source,
// reads any Sidecar at the same key, and returns the merged result. No
// DB, no queue, no service dependencies — just the I/O on src and store.
//
// The file's own extension wins over the caller's format slug when the
// key carries one, because the key is what the bytes actually are; the
// slug is what a row claims they are. store may be nil and key may be
// empty when the caller wants no Sidecar overlay.
//
// Errors are returned for transient failures (open, extract). A missing
// Sidecar is not an error — the result simply lacks the overlay. A
// malformed one is logged at the Sidecar layer and treated as absent.
func ExtractBook(
	ctx context.Context,
	store storage.Storage,
	src storage.Source,
	format string,
	key string,
) (ExtractResult, error) {
	if src == nil {
		return ExtractResult{}, errors.New("fileproc: nil source")
	}

	proc, resolved, err := dispatchKeyOrFormat(key, format)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("dispatch: %w", err)
	}
	if format == "" {
		format = resolved
	}

	meta, err := proc.Extract(ctx, src)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("extract: %w", err)
	}

	if store != nil && key != "" {
		if sc, sErr := sidecar.Read(ctx, store, key); sErr == nil && !sc.IsZero() {
			meta = layerSidecar(meta, sc)
		}
	}

	out := ExtractResult{
		Format:      format,
		Title:       meta.Title,
		Author:      meta.Author,
		Description: meta.Description,
		Language:    meta.Language,
		ISBN:        meta.ISBN,
		HasCover:    meta.HasCover,
		CoverBytes:  meta.CoverBytes,
		CoverMime:   meta.CoverMime,
	}
	// Audio takes a different ingest path: duration and narrator come from
	// tag metadata rather than a text extractor, and are meaningless on a
	// format that has no tags to read.
	if IsAudioFormat(format) {
		out.DurationSeconds = meta.DurationSeconds
		out.Narrator = meta.Narrator
	}
	return out, nil
}

// dispatchKeyOrFormat resolves a Processor from the storage key's
// extension, falling back to the format slug when the key has none this
// package recognises.
func dispatchKeyOrFormat(key, format string) (Processor, string, error) {
	if key != "" {
		if proc, resolved, err := Dispatch(key); err == nil {
			return proc, resolved, nil
		}
	}
	proc, err := DispatchFormat(format)
	if err != nil {
		return nil, "", err
	}
	return proc, strings.ToUpper(strings.TrimSpace(format)), nil
}

// layerSidecar overlays non-empty Sidecar fields onto metadata returned
// by the embedded extractor. Ground-truth-derived fields (cover bytes,
// duration, format) are never overwritten by Sidecar values.
func layerSidecar(m Metadata, s sidecar.Sidecar) Metadata {
	if s.Title != "" {
		m.Title = s.Title
	}
	if s.Author != "" {
		m.Author = s.Author
	}
	if s.Description != "" {
		m.Description = s.Description
	}
	if s.Language != "" {
		m.Language = s.Language
	}
	if s.ISBN != "" {
		m.ISBN = s.ISBN
	}
	return m
}
