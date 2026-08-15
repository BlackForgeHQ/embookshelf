// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fileproc extracts metadata and (eventually) covers from book files.
// Each supported format has a Processor; Dispatch picks the right one based
// on file extension.
package fileproc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// Metadata is the result of a successful extraction. Fields that the
// processor couldn't determine are left as zero values.
type Metadata struct {
	Title       string
	Author      string
	Description string
	Language    string
	// ISBN is the canonical identifier extracted from format-embedded
	// metadata (PDF XMP Identifier Bag, EPUB OPF identifier). Empty when
	// the file carries no ISBN. Format-specific extractors clean to digits
	// + 'X', then validate length 10 or 13 before populating; anything
	// else is dropped silently.
	ISBN string
	// HasCover reports whether the processor saw a cover image.
	HasCover bool
	// CoverBytes are the raw image bytes extracted from the file. nil when
	// HasCover is false. Passed through to coverstore; not rescaled.
	CoverBytes []byte
	// CoverMime is the cover's content type (e.g. "image/jpeg"). Filled
	// whenever CoverBytes is populated; used as the response Content-Type
	// when the cover is later served.
	CoverMime string
	// Format is the canonical format tag (EPUB, PDF, CBZ, ...). The caller
	// uses this to populate books.format when approving.
	Format string
	// DurationSeconds is populated only for audio formats (MP3, M4B). nil
	// when the processor couldn't determine a duration — the UI surfaces
	// "—" rather than implying a misleading "0".
	DurationSeconds *int
	// Narrator is populated only for audio formats. Empty for everything
	// else.
	Narrator string
}

// Processor is the strategy interface implemented per file format.
type Processor interface {
	Extract(ctx context.Context, src storage.Source) (Metadata, error)
}

// ErrUnsupportedFormat is returned by Dispatch when the extension is unknown.
var ErrUnsupportedFormat = errors.New("unsupported file format")

// processors is the one fact model.FormatSpecs cannot hold without
// inverting the import: which extension has an extractor. Everything
// else — which extensions are admitted, what format they stamp, which
// formats are audio — derives from the table (#308). Keyed by extension
// rather than format because the alias and the canonical form need not
// share an implementation. The three comic extensions share one on
// purpose: openComic classifies the bytes itself, so a .cbz that is
// really a RAR ingests as the RAR it is instead of failing the zip
// parser its name chose (#344).
var processors = map[string]func() Processor{
	".epub": func() Processor { return &EPUBProcessor{} },
	".pdf":  func() Processor { return &PDFProcessor{} },
	".cbz":  func() Processor { return &ComicProcessor{} },
	".cbr":  func() Processor { return &ComicProcessor{} },
	".cb7":  func() Processor { return &ComicProcessor{} },
	".mp3":  func() Processor { return &AudioProcessor{} },
	".m4a":  func() Processor { return &AudioProcessor{} },
	".m4b":  func() Processor { return &AudioProcessor{} },
	".fb2":  func() Processor { return &FB2Processor{} },
	// Two formats, one processor: AZW3 (KF8) is the same PalmDB container
	// and EXTH layout as MOBI, and the file version inside says which.
	".mobi": func() Processor { return &MOBIProcessor{} },
	".azw3": func() Processor { return &MOBIProcessor{} },
}

// NoProcessorError is Dispatch's answer for an extension the table
// admits but nothing extracts yet — a recognised format, refused with
// its own name instead of the generic unsupported message that used to
// make an admitted .cbr read like a typo'd extension. Reads as
// ErrUnsupportedFormat so the ingest worker's terminal-failure branch
// keeps firing: the same bytes refuse identically in thirty seconds.
type NoProcessorError struct {
	Format string
	Ext    string
}

func (e *NoProcessorError) Error() string {
	return fmt.Sprintf("no processor for %s (%s) yet — the format is recognised but cannot be ingested", e.Format, e.Ext)
}

func (e *NoProcessorError) Unwrap() error { return ErrUnsupportedFormat }

// Dispatch picks the right processor based on the file's extension.
func Dispatch(path string) (Processor, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	format := model.IngestFormatForExt(ext)
	if format == "" {
		return nil, strings.ToUpper(strings.TrimPrefix(ext, ".")), ErrUnsupportedFormat
	}
	p, ok := processors[ext]
	if !ok {
		return nil, format, &NoProcessorError{Format: format, Ext: ext}
	}
	return p(), format, nil
}

// FormatForExt returns the canonical format tag for a given extension, or
// an empty string for unknown/unsupported extensions. A derivation of
// the model table — the folding (.cbr → CBZ, .m4a → M4B) lives on the
// rows there.
func FormatForExt(ext string) string {
	return model.IngestFormatForExt(ext)
}

// SupportedExts is the set of file extensions the watcher should
// consider — the table's ingest axis, derived once at init.
var SupportedExts = model.IngestExtensions()

// IsSupported reports whether the file's extension is in SupportedExts.
func IsSupported(path string) bool {
	return model.IngestFormatForExt(filepath.Ext(path)) != ""
}

// IsAudioFormat reports whether a books.format value denotes an
// audiobook. Audio books take a different ingest path — duration and
// narrator come from tag metadata rather than a text extractor — so
// several packages need to ask this question.
//
// A derivation of the table's Audio column, kept case-sensitive on
// purpose: books.format stores the upper-case form, and a lower-case
// value reaching this predicate is a bug to surface, not absorb.
func IsAudioFormat(format string) bool {
	s, ok := model.LookupFormat(format)
	return ok && s.Audio && format == s.Format
}
