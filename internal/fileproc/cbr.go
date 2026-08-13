// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/nwaples/rardecode/v2"

	"github.com/blackforge/embookshelf/internal/storage"
)

// CBRProcessor extracts metadata and the cover from a RAR-packed comic
// (.cbr) — the same pages and the same optional ComicInfo.xml a .cbz
// holds, in the container WinRAR writes (#310).
//
// The rules are not repeated here: comic.go decides what the cover is and
// what ComicInfo means, and this file answers only the two questions the
// container alone can, which is the point of splitting them — a .cbr and
// a .cbz of the same comic must produce the same row, and they do because
// they run the same code.
//
// The format tag stays CBZ: model.FormatSpecs folds .cbz, .cbr and .cb7
// onto one row, so books.format says CBZ for all three and .cbr is what
// the file is called, not what the shelf calls it.
//
// RAR is sequential where ZIP is random-access, and that shapes the pass:
// an entry has no index to seek to, so this walks the archive once for
// the listing and once for the (at most two) entries it wants. Skipping
// packed data on a walk is a seek — rardecode uses the underlying reader
// when it is an io.Seeker, which a SectionReader over a Source is — so
// the listing costs the headers rather than the archive. Solid archives,
// where each file continues the previous one's dictionary, are the one
// case that cannot be skipped past; see rarComic.read.
type CBRProcessor struct{}

// cbrMaxSolidDrainBytes bounds what a solid archive may cost. In a solid
// archive a file's bytes depend on the files before it, so reaching the
// cover means decoding everything ahead of it — an amount of work chosen
// by whoever packed the archive. Past this much the walk gives up and the
// book keeps its metadata without a cover, which is the same degradation
// an unreadable cover entry gets. Non-solid archives never drain at all.
const cbrMaxSolidDrainBytes int64 = 512 << 20

func (CBRProcessor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	a, err := newRARComic(src)
	if err != nil {
		return Metadata{}, err
	}
	return extractComic(ctx, "cbr", a)
}

// rarComic is the RAR end of comicArchive: a listing taken up front, and
// a second walk per read.
type rarComic struct {
	src   storage.Source
	names []string
	// solid is true when any file in the archive continues the previous
	// file's dictionary, which decides whether the read walk may skip.
	solid bool
}

func newRARComic(src storage.Source) (*rarComic, error) {
	r, err := rarReader(src)
	if err != nil {
		return nil, cbrError("open cbr", err)
	}

	c := &rarComic{src: src}
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, cbrError("read cbr entries", err)
		}
		if h.IsDir {
			continue
		}
		// One locked file locks the comic: the pages are the book, and
		// there is nothing to import from an archive whose pages will not
		// open. Caught here, on the listing, so the answer is the same
		// whether or not the locked file is the one we wanted.
		if h.Encrypted || h.HeaderEncrypted {
			return nil, fmt.Errorf("cbr: %w", errEncryptedArchive)
		}
		if len(c.names) >= comicMaxEntries {
			return nil, fmt.Errorf("cbr: archive holds more than %d entries", comicMaxEntries)
		}
		c.names = append(c.names, h.Name)
		c.solid = c.solid || h.Solid
	}
	return c, nil
}

func (c *rarComic) entries() []string { return c.names }

func (c *rarComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	r, err := rarReader(c.src)
	if err != nil {
		return nil, cbrError("open cbr", err)
	}

	out := make(map[string][]byte, len(want))
	remaining := len(want)
	var drained int64

	for remaining > 0 {
		// Per entry, because a solid archive's walk is the one thing here
		// that can run long: the drain below decodes whatever the archive
		// put in front of the cover.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("cbr: %w", err)
		}

		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, cbrError("read cbr entries", err)
		}

		// A duplicate name is read once: the first entry wins, the same
		// way the listing's first match chose it.
		max, wanted := want[h.Name]
		if _, done := out[h.Name]; done {
			wanted = false
		}

		if wanted {
			b, rerr := readCappedEntry(r, h.Name, max)
			switch {
			case rerr == nil:
				out[h.Name] = b
				remaining--
			case isRARPasswordError(rerr):
				return nil, fmt.Errorf("cbr: %w", errEncryptedArchive)
			default:
				// An entry that will not decode is one missing field, not a
				// failed import: comic.go degrades around it. No `continue`
				// here — the drain below still has to run, because this is
				// exactly the case that leaves the reader mid-entry.
				slog.Warn("comic entry unreadable, dropped", "container", "cbr", "entry", h.Name, "err", rerr)
			}
		}

		// In a solid archive each file's output continues the previous
		// file's decoder window (rardecode resets that window only for
		// non-solid files), so an entry that is stepped over has to be
		// decoded anyway — and so does the tail of one we stopped short of,
		// which is why this sits after the read on every path rather than
		// inside its success branch. Non-solid archives skip by seeking and
		// never come through here at all.
		if c.solid && remaining > 0 {
			n, derr := io.Copy(io.Discard, io.LimitReader(r, cbrMaxSolidDrainBytes-drained))
			drained += n
			if derr != nil {
				// An entry we were only stepping over will not decode, so
				// nothing behind it can be trusted either. Still a
				// degradation rather than a failed item — the same answer a
				// bad entry inside a ZIP gets — so stop and return what was
				// already read.
				slog.Warn("comic entry would not decode; stopping the solid walk",
					"container", "cbr", "entry", h.Name, "err", derr, "missing", remaining)
				break
			}
			if drained >= cbrMaxSolidDrainBytes {
				slog.Warn("solid cbr exceeded the decode budget; giving up on the remaining entries",
					"drainedBytes", drained, "capBytes", cbrMaxSolidDrainBytes, "missing", remaining)
				break
			}
		}
	}
	return out, nil
}

func rarReader(src storage.Source) (*rardecode.Reader, error) {
	// A SectionReader, not the Source itself: rardecode wants an
	// io.Reader it can also seek forward on to skip packed data, and a
	// section over the whole object is both, without handing the library
	// a reader the caller still owns the offset of.
	return rardecode.NewReader(io.NewSectionReader(src, 0, src.Size()))
}

// cbrError names the container and the step, and translates rardecode's
// two encryption sentinels into the one this package answers with — a
// locked archive and a broken one are different things to whoever reads
// the failed BookDrop row.
func cbrError(op string, err error) error {
	if isRARPasswordError(err) {
		return fmt.Errorf("cbr: %w", errEncryptedArchive)
	}
	return fmt.Errorf("cbr: %s: %w", op, err)
}

func isRARPasswordError(err error) bool {
	return errors.Is(err, rardecode.ErrArchiveEncrypted) ||
		errors.Is(err, rardecode.ErrArchivedFileEncrypted) ||
		errors.Is(err, rardecode.ErrBadPassword)
}
