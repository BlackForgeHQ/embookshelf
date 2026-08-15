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

// rarComic is the RAR end of comicFile — the container a .cbr names, in
// the container WinRAR writes (#310). The cover rule, the ComicInfo
// mapping and the page order are comic.go's; this file answers only the
// questions the container alone can.
//
// RAR is sequential where ZIP is random-access, and that shapes the
// pass: an entry has no index to seek to, so this walks the archive once
// for the listing and once per read pass. Skipping packed data on a walk
// is a seek — rardecode uses the underlying reader when it is an
// io.Seeker, which a SectionReader over a Source is — so the listing
// costs the headers rather than the archive. Solid archives, where each
// file continues the previous one's dictionary, are the one case that
// cannot be skipped past; see stream, which is the single statement of
// that rule (#344 — it used to be written twice, and #310's bug was its
// placement).
type rarComic struct {
	src   storage.Source
	names []string
	// solid is true when any file in the archive continues the previous
	// file's dictionary, which decides whether the read walk may skip.
	solid bool
}

// cbrMaxSolidDrainBytes bounds what a solid archive may cost. In a solid
// archive a file's bytes depend on the files before it, so reaching the
// cover means decoding everything ahead of it — an amount of work chosen
// by whoever packed the archive. Past this much the walk gives up and the
// book keeps its metadata without a cover, which is the same degradation
// an unreadable cover entry gets. Non-solid archives never drain at all.
const cbrMaxSolidDrainBytes int64 = 512 << 20
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

func (c *rarComic) kind() string { return "cbr" }

func (c *rarComic) entries() []string { return c.names }

// read buffers the wanted entries through the one walk stream owns. A
// password error mid-entry fails the archive — the first entry of an
// encrypted block opens and then fails mid-read — which is walkerRead's
// encrypted arm.
func (c *rarComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	return walkerRead(ctx, "cbr", c, want, isRARPasswordError)
}

// stream hands every wanted entry's bytes to the sink in one walk. It is
// the container's only walk: the paging pass consumes it directly and
// read buffers through it (walkerRead), so the solid-drain rule below is
// stated once — #310's bug was this rule's placement, and until #344 it
// had two homes to be wrong in.
//
// Extracting *every* page means the drain almost never runs: consecutive
// wanted entries continue each other's dictionary as they are read, so
// only the non-image entries between them (a ComicInfo.xml, a thumbs
// directory) are stepped over. A sink that stops short of an entry's end
// leaves the reader mid-entry, which is exactly what the drain's
// placement — after the sink, on every path — exists for.
func (c *rarComic) stream(ctx context.Context, want map[string]bool, sink pageSink) error {
	r, err := rarReader(c.src)
	if err != nil {
		return cbrError("open cbr", err)
	}

	done := make(map[string]bool, len(want))
	remaining := len(want)
	var drained int64

	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cbr: %w", err)
		}

		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return cbrError("read cbr entries", err)
		}

		if want[h.Name] && !done[h.Name] {
			if err := sink(h.Name, r); err != nil {
				return err
			}
			done[h.Name] = true
			remaining--
		}

		// Same rule as read: in a solid archive an entry stepped over
		// still has to be decoded, and so does the tail of one the sink
		// stopped short of, so this sits after the sink on every path.
		if c.solid && remaining > 0 {
			n, derr := io.Copy(io.Discard, io.LimitReader(r, cbrMaxSolidDrainBytes-drained))
			drained += n
			if derr != nil {
				slog.Warn("comic entry would not decode; stopping the solid walk",
					"container", "cbr", "entry", h.Name, "err", derr, "missing", remaining)
				break
			}
			if drained >= cbrMaxSolidDrainBytes {
				slog.Warn("solid cbr exceeded the decode budget; giving up on the remaining pages",
					"drainedBytes", drained, "capBytes", cbrMaxSolidDrainBytes, "missing", remaining)
				break
			}
		}
	}
	return nil
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
