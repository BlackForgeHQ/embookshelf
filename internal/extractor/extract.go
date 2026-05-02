package extractor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/sidecar"
	"github.com/blackforge/embookshelf/internal/storage"
)

// ExtractResult is the unified output of one extraction pass over a
// book file: format-specific metadata from the embedded extractor,
// the cover bytes (if any), audio-only fields, and the merged sidecar
// overlay. It is the single contract shared by the bookdrop ingest
// path and the (future) scan-direct ScanImportJob, per ADR-0004 §2.
type ExtractResult struct {
	Format          string
	Title           string
	Author          string
	Description     string
	Language        string
	HasCover        bool
	CoverBytes      []byte
	CoverMime       string
	// DurationSeconds is non-nil only for audio formats with a
	// readable header.
	DurationSeconds *int
	Narrator        string
}

// Extract runs the format-specific processor against an open Source,
// reads any sidecars at the same key location, and returns the
// merged result. Pure function modulo the I/O on src and store —
// no DB, no queue, no service dependencies.
//
// store may be nil when the caller does not want sidecar overlays
// (rare; bookdrop ingest always passes one). format is the
// books.format slug ("EPUB", "PDF", …) used to gate audio-only
// fields. key is the storage key of the book file (used for
// sidecar lookup); pass "" to skip sidecar reads.
//
// Errors are returned for transient failures (open, extract). A
// missing sidecar is not an error — the result simply lacks the
// overlay. A malformed sidecar is logged at the sidecar layer and
// treated as absent.
func Extract(
	ctx context.Context,
	store storage.Storage,
	src storage.Source,
	format string,
	key string,
) (ExtractResult, error) {
	if src == nil {
		return ExtractResult{}, errors.New("ingest: nil source")
	}

	proc, dispatchedFormat, err := fileproc.Dispatch(formatToPath(format, key))
	if err != nil {
		return ExtractResult{}, fmt.Errorf("dispatch: %w", err)
	}
	if format == "" {
		format = dispatchedFormat
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
		HasCover:    meta.HasCover,
		CoverBytes:  meta.CoverBytes,
		CoverMime:   meta.CoverMime,
	}
	if isAudioFormat(format) {
		out.DurationSeconds = meta.DurationSeconds
		out.Narrator = meta.Narrator
	}
	return out, nil
}

// formatToPath synthesizes a path string suitable for fileproc.Dispatch
// when the caller has only a format slug + storage key. Dispatch keys
// off the file extension; we prefer the actual key when available so
// the dispatcher's "EPUB by zip-magic" path stays available, falling
// back to a slug-derived stub when the caller passed an empty key.
func formatToPath(format, key string) string {
	if key != "" {
		return key
	}
	switch strings.ToUpper(format) {
	case "EPUB":
		return "x.epub"
	case "PDF":
		return "x.pdf"
	case "CBZ":
		return "x.cbz"
	case "MP3":
		return "x.mp3"
	case "M4B":
		return "x.m4b"
	case "AZW3":
		return "x.azw3"
	case "MOBI":
		return "x.mobi"
	case "FB2":
		return "x.fb2"
	}
	return ""
}

// layerSidecar overlays non-empty sidecar fields onto metadata returned
// by the embedded extractor. Only fields the sidecar carries are
// considered; ground-truth-derived fields (cover bytes, duration,
// format) are never overwritten by sidecar values.
func layerSidecar(m fileproc.Metadata, s sidecar.Sidecar) fileproc.Metadata {
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
	return m
}

// isAudioFormat reports whether the given books.format slug names an
// audio file the AudioProcessor extracts metadata from.
func isAudioFormat(f string) bool {
	switch f {
	case "MP3", "M4B":
		return true
	}
	return false
}
