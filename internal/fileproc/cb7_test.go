// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The .cb7 fixtures under testdata/ are real 7-Zip archives, checked in
// because there is no pure-Go 7z *writer* (sevenzip reads only) and the
// container's header is not something to hand-assemble in a test the way
// cbr_test.go assembles RAR's. They were made with 7-Zip 26.02 (`brew
// install sevenzip`) from four files:
//
//	ComicInfo.xml  the comicInfoFixtureXML constant, verbatim
//	notes.txt      "skip"        — a non-image, to be ignored
//	page10.png     "ten"         — sorts after page2 naturally, before it lexically
//	page2.png      "page-two"    — therefore the cover
//
//	7zz a -mx=9 comic.cb7 ComicInfo.xml notes.txt page10.png page2.png
//	7zz a -mx=9 -psecret comic-encrypted.cb7 <same four>
//	7zz a -mx=9 -psecret -mhe=on comic-headers-encrypted.cb7 <same four>
//
// comic-cover.cb7 is the other order-sensitive shape — several pages plus
// a top-level cover.png that is neither first entry nor first page, the
// contents of comicCoverFixtureEntries:
//
//	7zz a -mx=9 comic-cover.cb7 ComicInfo.xml page10.png page1.png cover.png page2.png
//
// and comic-bomb.cb7 from ComicInfo.xml plus a 64 MiB 01.png of zeros
// (`dd if=/dev/zero of=01.png bs=1m count=64`), which is 10 KB packed —
// a decompression bomb small enough to commit.
const (
	cb7Fixture          = "testdata/comic.cb7"
	cb7CoverFixture     = "testdata/comic-cover.cb7"
	cb7EncryptedFixture = "testdata/comic-encrypted.cb7"
	cb7HeaderEncFixture = "testdata/comic-headers-encrypted.cb7"
	cb7BombFixture      = "testdata/comic-bomb.cb7"
)

// The same comic, packed by a third archiver, produces the same row: the
// natural-sort cover and the ComicInfo mapping are comic.go's, not the
// container's.
func TestCB7Extract_CoverAndComicInfo(t *testing.T) {
	src := memSourceFromFile(t, cb7Fixture)
	defer func() { _ = src.Close() }()

	meta, err := CB7Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.Format != "CBZ" {
		t.Errorf("Format = %q, want CBZ — the table folds .cb7 onto CBZ", meta.Format)
	}
	assertComicInfoFixture(t, meta)
	if !meta.HasCover {
		t.Fatal("expected HasCover")
	}
	if string(meta.CoverBytes) != "page-two" {
		t.Errorf("CoverBytes = %q, want page2.png's bytes — natural sort failed", meta.CoverBytes)
	}
	if meta.CoverMime != "image/png" {
		t.Errorf("CoverMime = %q, want image/png", meta.CoverMime)
	}
}

// 7z encrypts in two places, and both have to answer the same way: with
// -p the entries are locked but the listing is readable, with -mhe=on the
// header itself is locked and the archive cannot even be listed.
func TestCB7Extract_PasswordProtected(t *testing.T) {
	for name, fixture := range map[string]string{
		"contents": cb7EncryptedFixture,
		"headers":  cb7HeaderEncFixture,
	} {
		t.Run(name, func(t *testing.T) {
			src := memSourceFromFile(t, fixture)
			defer func() { _ = src.Close() }()

			_, err := CB7Processor{}.Extract(context.Background(), src)
			if err == nil {
				t.Fatal("expected an error for a password-protected archive")
			}
			if !errors.Is(err, errEncryptedArchive) {
				t.Errorf("err = %v, want it to read as errEncryptedArchive", err)
			}
			if !strings.Contains(err.Error(), "cb7") {
				t.Errorf("err = %q, want the container named", err)
			}
		})
	}
}

// A page that unpacks to 64 MiB from 10 KB of archive is dropped at the
// cover cap, and the book keeps the metadata it does have. The assertion
// that matters is that this returns at all: an unbounded read here is 64
// MiB of zeros in the ingest worker's heap for every such file dropped.
func TestCB7Extract_DecompressionBombIsBounded(t *testing.T) {
	src := memSourceFromFile(t, cb7BombFixture)
	defer func() { _ = src.Close() }()

	meta, err := CB7Processor{}.Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if meta.HasCover || meta.CoverBytes != nil {
		t.Errorf("buffered %d cover bytes past the %d byte cap", len(meta.CoverBytes), comicMaxCoverBytes)
	}
	assertComicInfoFixture(t, meta)
}

// Corrupt bytes fail the item with a message, never a panic.
func TestCB7Extract_CorruptFails(t *testing.T) {
	good, err := os.ReadFile(filepath.FromSlash(cb7Fixture))
	if err != nil {
		t.Fatal(err)
	}

	truncated := append([]byte(nil), good[:len(good)/2]...)

	// The signature header carries the offset and CRC of the real header;
	// a flipped byte inside it points the reader at nothing.
	flipped := append([]byte(nil), good...)
	flipped[20] ^= 0xFF

	cases := map[string][]byte{
		"not a 7z":  []byte(strings.Repeat("7", 512)),
		"empty":     nil,
		"truncated": truncated,
		"flipped":   flipped,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			src := memSourceFromBytes(raw)
			defer func() { _ = src.Close() }()
			meta, err := CB7Processor{}.Extract(context.Background(), src)
			if err == nil {
				t.Fatalf("expected an error, got %+v", meta)
			}
			if meta.HasCover || meta.Title != "" {
				t.Errorf("failed extraction returned metadata: %+v", meta)
			}
		})
	}
}
