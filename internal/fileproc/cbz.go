// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"

	"github.com/blackforge/embookshelf/internal/storage"
)

// ErrComicNotZIP is what the page reader answers for a comic whose bytes
// are not a ZIP at all. It exists because the three comic extensions all
// stamp books.format = CBZ (model.FormatSpecs) and all reach the shelf
// since #310, while only the ZIP one can serve a numbered page cheaply —
// so a .cbr book opens the comic reader and this is the answer it gets. A
// distinct error rather than a 500 with a zip parser's complaint in it:
// "we cannot page through this comic" is a fact about the file, and the
// reader UI can say so.
//
// Reserved for bytes that are not a ZIP. A damaged .cbz is a ZIP that
// would not open, which is a different sentence and a different status —
// telling its owner the file "isn't a .cbz" would send them looking for a
// conversion they do not need. openComicZip is where the two are told
// apart, by the archive's own magic rather than by the parser's mood.
var ErrComicNotZIP = errors.New("not a ZIP-packed comic: pages can be served from .cbz only")

// zipLocalFileMagic and zipEmptyMagic are the two signatures a ZIP can
// start with: a first local file header, or the end-of-central-directory
// record of an archive with no entries. (The third, PK\x07\x08, only
// heads a spanned archive's first segment, which is not a comic anyone
// ships.) Read from the front rather than from the central directory at
// the tail, because the question here is what the file *is*, and a
// truncated ZIP has lost its tail — which is exactly the case that must
// not be mistaken for a RAR.
var (
	zipLocalFileMagic = []byte{'P', 'K', 0x03, 0x04}
	zipEmptyMagic     = []byte{'P', 'K', 0x05, 0x06}
)

// openComicZip opens a comic archive and classifies the failure: bytes
// that are not a ZIP get ErrComicNotZIP (415 at the handler — the file is
// a comic in a container the page endpoints do not serve), and a ZIP that
// would not open keeps the error it always had (500 — the file is the
// right shape and damaged).
func openComicZip(src storage.Source) (*zip.Reader, error) {
	zr, err := zip.NewReader(src, src.Size())
	if err == nil {
		return zr, nil
	}
	if hasZipMagic(src) {
		return nil, fmt.Errorf("open cbz: %w", err)
	}
	return nil, fmt.Errorf("%w (%v)", ErrComicNotZIP, err)
}

func hasZipMagic(src storage.Source) bool {
	var head [4]byte
	if _, err := src.ReadAt(head[:], 0); err != nil {
		return false
	}
	return bytes.Equal(head[:], zipLocalFileMagic) || bytes.Equal(head[:], zipEmptyMagic)
}

// CBZProcessor extracts metadata and cover image from a CBZ comic archive.
//
// CBZ is a ZIP of page images, sorted by filename. Some scene-released
// archives also embed a `ComicInfo.xml` with series/issue/year/summary —
// when present we surface those into the regular metadata fields so the
// library UI shows useful info without manual enrichment.
//
// Cover = the first page after natural sort, OR a file matching `cover.*`
// at the archive root if present. Those rules, and the ComicInfo mapping,
// live in comic.go: they are the same for a comic packed as RAR or 7z
// (#310), and this file is only the ZIP end of them.
type CBZProcessor struct{}

func (CBZProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return Metadata{}, fmt.Errorf("open cbz: %w", err)
	}
	// zr is *zip.Reader (not *zip.ReadCloser); no Close needed.
	// The caller is responsible for closing the Source.

	return extractComic(ctx, "cbz", &zipComic{zr: zr})
}

// zipComic is the ZIP end of comicArchive. Random access, so a read is a
// direct lookup per wanted entry.
type zipComic struct {
	zr *zip.Reader
}

func (z *zipComic) entries() []string {
	names := make([]string, 0, len(z.zr.File))
	for _, f := range z.zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, f.Name)
	}
	return names
}

func (z *zipComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	out := make(map[string][]byte, len(want))
	for _, f := range z.zr.File {
		max, ok := want[f.Name]
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("cbz: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			slog.Warn("comic entry would not open, dropped", "container", "cbz", "entry", f.Name, "err", err)
			continue
		}
		b, err := readCappedEntry(rc, f.Name, max)
		_ = rc.Close()
		if err != nil {
			slog.Warn("comic entry unreadable, dropped", "container", "cbz", "entry", f.Name, "err", err)
			continue
		}
		out[f.Name] = b
	}
	// No archive-wide failure mode: a ZIP that opened has a directory,
	// and a bad entry inside it is the per-entry degradation above.
	// Encrypted ZIPs are not a case archive/zip can even report — it
	// refuses them at Open, one level up.
	return out, nil
}

// CBZPages returns the archive's page entry names in natural sort order.
// Used by the page-streaming handler so the reader can resolve "page n"
// to a real archive entry without re-listing on every request (callers
// are expected to cache the slice).
//
// Takes a storage.Source, like every other reader in this package, so a
// comic is readable wherever its bytes live. It used to take a filesystem
// path, which meant pagination could not be attempted at all on an
// object-store-backed library and nothing said so (#240).
//
// A Source is a ReaderAt, which is what a zip actually needs: the central
// directory sits at the tail, so listing costs a read of the tail rather
// than of the object. The caller owns the Source and may reuse one across
// a list and several page reads.
//
// ZIP only, deliberately: this is the reader's paging seam, and a RAR or
// 7z comic reaches the shelf through the processors in cbr.go/cb7.go but
// not through here — neither container serves a random page for the price
// a ZIP does.
func CBZPages(src storage.Source) ([]string, error) {
	zr, err := openComicZip(src)
	if err != nil {
		return nil, err
	}
	return comicPages((&zipComic{zr: zr}).entries()), nil
}

// CBZPage copies the n-th page (0-indexed, natural sort order) into w.
// Returns the resolved MIME type so the caller can set the response
// Content-Type. Errors when n is out of range.
//
// Reads one entry, not the archive: over an object store this is a range
// read of that page's bytes plus the directory, so serving page 400 of a
// 600 MB archive does not cost 600 MB.
func CBZPage(src storage.Source, n int, w io.Writer) (mime string, err error) {
	zr, err := openComicZip(src)
	if err != nil {
		return "", err
	}

	pages := comicPages((&zipComic{zr: zr}).entries())
	if n < 0 || n >= len(pages) {
		return "", fmt.Errorf("page %d out of range (0..%d)", n, len(pages)-1)
	}
	name := pages[n]
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open page: %w", err)
		}
		defer func() { _ = rc.Close() }()
		if _, err := io.Copy(w, rc); err != nil {
			return "", fmt.Errorf("stream page: %w", err)
		}
		return mimeFromExt(path.Ext(name)), nil
	}
	return "", fmt.Errorf("page %d entry %q vanished", n, name)
}
