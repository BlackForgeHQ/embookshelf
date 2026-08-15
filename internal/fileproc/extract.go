// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// DispatchFormat returns the Processor for a books.format slug.
//
// The slug-keyed twin of Dispatch, which keys off a file extension. Both
// live here because format dispatch is this package's job; the slug entry
// point used to sit one package away and synthesize a fake filename
// ("x.epub") to feed the extension dispatcher — a module converting its
// own input backwards to satisfy the interface it wrapped.
func DispatchFormat(format string) (Processor, error) {
	spec, ok := model.LookupFormat(format)
	if !ok {
		return nil, ErrUnsupportedFormat
	}
	// The format's canonical extension carries its processor entry; a
	// format whose extractor is still unwired (#310–#312) answers the
	// same refusal the extension entry point gives.
	p, ok := processors[spec.Ext]
	if !ok {
		return nil, &NoProcessorError{Format: spec.Format, Ext: spec.Ext}
	}
	return p(), nil
}

// ExtractBook runs the format-specific Processor against an open Source,
// reads any Sidecar at the same key, and returns the merged Metadata. No
// DB, no queue, no service dependencies — just the I/O on src and store.
// Sole consumer is the BookDrop ingest path — under ADR-0018, Library
// scan never extracts. (It used to return its own 12-field copy of
// Metadata; the copy carried one consumer and no behaviour — #335.)
//
// The file's own extension wins over the caller's format slug when the
// key carries one, because the key is what the bytes actually are; the
// slug is what a row claims they are. store may be nil and key may be
// empty when the caller wants no Sidecar overlay. The returned Format is
// the caller's slug when one was given, the key-resolved format
// otherwise.
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
) (Metadata, error) {
	if src == nil {
		return Metadata{}, errors.New("fileproc: nil source")
	}

	proc, resolved, err := dispatchKeyOrFormat(key, format)
	if err != nil {
		return Metadata{}, fmt.Errorf("dispatch: %w", err)
	}
	if format == "" {
		format = resolved
	}

	meta, err := proc.Extract(ctx, src)
	if err != nil {
		return Metadata{}, fmt.Errorf("extract: %w", err)
	}

	if store != nil && key != "" {
		if sc, sErr := sidecar.Read(ctx, store, key); sErr == nil && !sc.IsZero() {
			meta = layerSidecar(meta, sc)
		}
	}

	// The cover's content type is re-derived from its own bytes here,
	// after every processor and the Sidecar overlay have had their say
	// and before anything is persisted. Whatever a processor put in
	// CoverMime came from the file's author — a manifest media-type, an
	// ID3 MIME field, an archive entry's extension — and the cover
	// routes serve that string back as the response Content-Type. This
	// is the seam where that stops being true (#330).
	meta = normalizeCover(meta)

	meta.Format = format
	return gateAudioFields(meta, format), nil
}

// gateAudioFields zeroes the audio-only fields for a non-audio format.
// Audio takes a different ingest path: duration and narrator come from
// tag metadata rather than a text extractor, and are meaningless on a
// format that has no tags to read — including the drift case where a
// row claims a non-audio format over bytes that dispatched to the audio
// extractor by their key (#335).
func gateAudioFields(m Metadata, format string) Metadata {
	if !IsAudioFormat(format) {
		m.DurationSeconds = nil
		m.Narrator = ""
	}
	return m
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
