// Package fileproc extracts metadata and (eventually) covers from book files.
// Each supported format has a Processor; Dispatch picks the right one based
// on file extension.
package fileproc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

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

// Dispatch picks the right processor based on the file's extension.
func Dispatch(path string) (Processor, string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".epub":
		return &EPUBProcessor{}, "EPUB", nil
	case ".pdf":
		return &PDFProcessor{}, "PDF", nil
	case ".cbz":
		return &CBZProcessor{}, "CBZ", nil
	case ".mp3":
		return &AudioProcessor{}, "MP3", nil
	case ".m4a", ".m4b":
		return &AudioProcessor{}, "M4B", nil
	// TODO: cbr, azw3, mobi, fb2
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
	case "mp3":
		return "MP3"
	case "m4a", "m4b":
		return "M4B"
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
