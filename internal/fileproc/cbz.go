// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/storage"
)

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

// The reader's ZIP paging arm lives in comicpaging.go, which is where
// the other two containers' arms had to join it (#329). It still reads
// one entry rather than the archive: over an object store that is a
// range read of that page's bytes plus the directory, so serving page
// 400 of a 600 MB archive does not cost 600 MB — the property that keeps
// ZIP out of the page cache the other two need.
