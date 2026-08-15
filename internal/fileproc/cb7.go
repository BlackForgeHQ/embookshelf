// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bodgit/sevenzip"

	"github.com/blackforge/embookshelf/internal/storage"
)

// sevenzipComic is the 7z end of comicFile — the third container the
// same pages ship in, after ZIP and RAR (#310). The cover rule and the
// ComicInfo mapping are comic.go's; this file is the 7z end of them.
//
// 7z is random-access like ZIP — the header at the tail names every entry
// and which compressed block holds it — so a read is a direct lookup and
// there is no walk to pay for. What it has that ZIP does not is
// encryption, in two places: the entries, or the header naming them. Both
// end at the same answer, because a comic whose pages will not open
// without a password is not a comic this can import.
type sevenzipComic struct {
	zr *sevenzip.Reader
}

// newSevenzipComic opens a 7z comic. The whole Source as a ReaderAt,
// which is what 7z wants: the header is at the tail, so opening costs a
// read of the tail rather than of the object.
//
// The entry-count check is a rejection, not a bound: sevenzip has
// already parsed the header and materialised this slice by the time we
// look, so the allocation the cap is about has happened. What it buys is
// that the passes above do not go on to sort and scan a list that size —
// and that an archive this shape is refused with a sentence rather than
// worked on. Bounding the parse itself is sevenzip's business, one layer
// down.
func newSevenzipComic(src storage.Source) (*sevenzipComic, error) {
	zr, err := sevenzip.NewReader(src, src.Size())
	if err != nil {
		return nil, cb7Error("open cb7", err)
	}
	if len(zr.File) > comicMaxEntries {
		return nil, fmt.Errorf("cb7: archive holds more than %d entries", comicMaxEntries)
	}
	return &sevenzipComic{zr: zr}, nil
}

// stream hands every wanted entry's bytes to the sink, walking the
// archive in its own order (#329).
//
// Archive order, not page order, and that is the whole trick: a comic's
// pages usually sit in one solid folder, and sevenzip's folder-reader
// pool hands a request for offset N the open decoder that stopped just
// below it. Walked forwards, the entire extraction is one decode of the
// folder; walked in natural sort order — or one entry per request, with
// a fresh Reader each time, which is what paging without a cache would
// do — it is one decode per page.
func (c *sevenzipComic) stream(ctx context.Context, want map[string]bool, sink pageSink) error {
	done := make(map[string]bool, len(want))
	for _, f := range c.zr.File {
		name := cb7Name(f)
		if !want[name] || done[name] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("cb7: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			if cb7Encrypted(err) {
				return fmt.Errorf("cb7: %w", errEncryptedArchive)
			}
			// One page that will not open is one page missing, not a
			// failed comic: the sink is not called and the slot stays
			// empty, so the pages around it still answer.
			slog.Warn("comic entry would not open, dropped", "container", "cb7", "entry", name, "err", err)
			continue
		}
		serr := sink(name, rc)
		_ = rc.Close()
		if serr != nil {
			return serr
		}
		done[name] = true
	}
	return nil
}

func (c *sevenzipComic) kind() string { return "cb7" }

func (c *sevenzipComic) entries() []string {
	names := make([]string, 0, len(c.zr.File))
	for _, f := range c.zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		names = append(names, cb7Name(f))
	}
	return names
}

// read buffers the wanted entries through stream's walk. The first entry
// of an encrypted block opens and then fails mid-read, so the encryption
// check sits on the buffered read too — walkerRead's encrypted arm; the
// open-time half already lives in stream.
func (c *sevenzipComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	return walkerRead(ctx, "cb7", c, want, cb7Encrypted)
}

// cb7Name normalises an entry name to slash separators. 7-Zip on Windows
// writes the paths it was given, backslashes included, and every rule
// above this — the cover's directory, the ComicInfo base name, the
// extension — is written in terms of slash-separated paths.
func cb7Name(f *sevenzip.File) string {
	return strings.ReplaceAll(f.Name, `\`, "/")
}

// cb7Error names the container and the step, and answers a locked archive
// with the shared encryption error rather than the decoder's complaint
// about the ciphertext it was handed.
func cb7Error(op string, err error) error {
	if cb7Encrypted(err) {
		return fmt.Errorf("cb7: %w", errEncryptedArchive)
	}
	return fmt.Errorf("cb7: %s: %w", op, err)
}

// cb7Encrypted reports whether a failure is 7z telling us it wanted a
// password. sevenzip flags that on its own error type rather than with a
// sentinel, because what surfaces underneath is whatever the decoder made
// of undecrypted bytes — "unsupported chunk header byte" and the like,
// which is not a message to put on a BookDrop row.
func cb7Encrypted(err error) bool {
	var re *sevenzip.ReadError
	return errors.As(err, &re) && re.Encrypted
}
