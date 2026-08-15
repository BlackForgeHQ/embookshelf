// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"

	"github.com/blackforge/embookshelf/internal/storage"
)

// zipComic is the ZIP end of comicFile — the container a .cbz names,
// though what a file is called does not decide what opens it (openComic
// reads the magic). Random access, so both passes are direct lookups:
// no drain, no decode of anything not asked for.
//
// The cover rule, the ComicInfo mapping and the page order live in
// comic.go: they are the same for a comic packed as RAR or 7z (#310),
// and this file is only the ZIP end of them.
type zipComic struct {
	zr *zip.Reader
}

// newZipComic opens a ZIP comic. The entry-count check is the same
// rejection newSevenzipComic makes and for the same reason: archive/zip
// has materialised the list by now, so what the cap buys is that the
// passes above never sort and scan a list that size — and that all
// three containers refuse the same shape of archive with the same
// sentence (#344; ZIP used to be the one container without it).
func newZipComic(src storage.Source) (*zipComic, error) {
	zr, err := zip.NewReader(src, src.Size())
	if err != nil {
		return nil, fmt.Errorf("open cbz: %w", err)
	}
	if len(zr.File) > comicMaxEntries {
		return nil, fmt.Errorf("cbz: archive holds more than %d entries", comicMaxEntries)
	}
	return &zipComic{zr: zr}, nil
}

func (z *zipComic) kind() string { return "cbz" }

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

// stream hands every wanted entry's bytes to the sink in archive order.
// One walk like its two siblings, though here each open is a direct
// lookup — the loop shape is shared so the read and the paging pass ask
// one implementation, not because ZIP needs the single pass.
func (z *zipComic) stream(ctx context.Context, want map[string]bool, sink pageSink) error {
	done := make(map[string]bool, len(want))
	for _, f := range z.zr.File {
		if !want[f.Name] || done[f.Name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cbz: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			// One entry that will not open is one page or field missing,
			// not a failed comic. Encrypted ZIPs are not a case archive/zip
			// can even report at NewReader; a flagged entry surfaces here
			// and degrades the same way.
			slog.Warn("comic entry would not open, dropped", "container", "cbz", "entry", f.Name, "err", err)
			continue
		}
		serr := sink(f.Name, rc)
		_ = rc.Close()
		if serr != nil {
			return serr
		}
		done[f.Name] = true
	}
	return nil
}

func (z *zipComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	return walkerRead(ctx, "cbz", z, want, nil)
}

// The reader's ZIP paging arm lives in comicpaging.go, which is where
// the other two containers' arms had to join it (#329). It still reads
// one entry rather than the archive: over an object store that is a
// range read of that page's bytes plus the directory, so serving page
// 400 of a 600 MB archive does not cost 600 MB — the property that keeps
// ZIP out of the page cache the other two need.
