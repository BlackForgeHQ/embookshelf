// Package fileproc extracts metadata and (eventually) covers from book files.
// Each supported format has a Processor; Dispatch picks the right one based
// on file extension.
package fileproc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// Metadata is the result of a successful extraction. Fields that the
// processor couldn't determine are left as zero values.
type Metadata struct {
	Title       string
	Author      string
	Description string
	Language    string
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
}

// Processor is the strategy interface implemented per file format.
type Processor interface {
	Extract(ctx context.Context, path string) (Metadata, error)
}

// ErrUnsupportedFormat is returned by Dispatch when the extension is unknown.
var ErrUnsupportedFormat = errors.New("unsupported file format")

// Dispatch picks the right processor based on the file's extension.
func Dispatch(path string) (Processor, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".epub":
		return &EPUBProcessor{}, "EPUB", nil
	// TODO: pdf, cbz, cbr, mp3, m4b, azw3, mobi, fb2
	default:
		return nil, strings.ToUpper(strings.TrimPrefix(ext, ".")), ErrUnsupportedFormat
	}
}

// FormatForExt returns the canonical format tag for a given extension, or
// an empty string for unknown/unsupported extensions.
func FormatForExt(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "epub":
		return "EPUB"
	case "pdf":
		return "PDF"
	case "cbz", "cbr", "cb7":
		return "CBZ"
	case "mp3", "m4a", "m4b":
		return "MP3"
	case "mobi":
		return "MOBI"
	case "azw3":
		return "AZW3"
	case "fb2":
		return "FB2"
	}
	return ""
}

// SupportedExts is the set of file extensions the watcher should consider.
var SupportedExts = []string{
	".epub", ".pdf",
	".cbz", ".cbr", ".cb7",
	".mp3", ".m4a", ".m4b",
	".mobi", ".azw3", ".fb2",
}

// IsSupported reports whether the file's extension is in SupportedExts.
func IsSupported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range SupportedExts {
		if e == ext {
			return true
		}
	}
	return false
}
