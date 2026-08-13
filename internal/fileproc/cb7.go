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

// CB7Processor extracts metadata and the cover from a 7z-packed comic
// (.cb7) — the third container the same pages ship in, after ZIP and RAR
// (#310). The cover rule and the ComicInfo mapping are comic.go's; this
// file is the 7z end of them, and the format tag stays CBZ because that
// is the row model.FormatSpecs folds all three extensions onto.
//
// 7z is random-access like ZIP — the header at the tail names every entry
// and which compressed block holds it — so a read is a direct lookup and
// there is no walk to pay for. What it has that ZIP does not is
// encryption, in two places: the entries, or the header naming them. Both
// end at the same answer, because a comic whose pages will not open
// without a password is not a comic this can import.
type CB7Processor struct{}

func (CB7Processor) Extract(ctx context.Context, src storage.Source) (Metadata, error) {
	// The whole Source as a ReaderAt, which is what 7z wants: the header
	// is at the tail, so opening costs a read of the tail rather than of
	// the object, and each entry costs its own block.
	zr, err := sevenzip.NewReader(src, src.Size())
	if err != nil {
		return Metadata{}, cb7Error("open cb7", err)
	}
	// A rejection, not a bound: sevenzip has already parsed the header and
	// materialised this slice by the time we look, so the allocation the
	// cap is about has happened. What it buys is that the pass above does
	// not go on to sort and scan a list that size — and that an archive
	// this shape is refused with a sentence rather than worked on.
	// Bounding the parse itself is sevenzip's business, one layer down.
	if len(zr.File) > comicMaxEntries {
		return Metadata{}, fmt.Errorf("cb7: archive holds more than %d entries", comicMaxEntries)
	}
	return extractComic(ctx, "cb7", &sevenzipComic{zr: zr})
}

// sevenzipComic is the 7z end of comicArchive.
type sevenzipComic struct {
	zr *sevenzip.Reader
}

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

func (c *sevenzipComic) read(ctx context.Context, want map[string]int64) (map[string][]byte, error) {
	out := make(map[string][]byte, len(want))
	for _, f := range c.zr.File {
		name := cb7Name(f)
		max, ok := want[name]
		if !ok {
			continue
		}
		if _, done := out[name]; done {
			continue
		}
		// Opening an entry in a solid block decodes the block up to it,
		// so a read here is not free either.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("cb7: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			if cb7Encrypted(err) {
				return nil, fmt.Errorf("cb7: %w", errEncryptedArchive)
			}
			// An entry that will not open is one missing field, not a
			// failed import: comic.go degrades around it.
			slog.Warn("comic entry would not open, dropped", "container", "cb7", "entry", name, "err", err)
			continue
		}
		b, rerr := readCappedEntry(rc, name, max)
		_ = rc.Close()
		if rerr != nil {
			// The first entry of an encrypted block opens and then fails
			// mid-read, so the encryption check has to sit on both.
			if cb7Encrypted(rerr) {
				return nil, fmt.Errorf("cb7: %w", errEncryptedArchive)
			}
			slog.Warn("comic entry unreadable, dropped", "container", "cb7", "entry", name, "err", rerr)
			continue
		}
		out[name] = b
	}
	return out, nil
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
