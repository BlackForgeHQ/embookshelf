// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// openCounter hands out sources over one archive and counts how many
// times it was asked. It is the whole assertion behind "extract once":
// a comic served from the cache never reaches for its bytes again.
type openCounter struct {
	raw   []byte
	opens atomic.Int64
	reads atomic.Int64
}

func (o *openCounter) open() (storage.Source, error) {
	o.opens.Add(1)
	return &countedSource{Reader: bytes.NewReader(o.raw), size: int64(len(o.raw)), owner: o}, nil
}

type countedSource struct {
	*bytes.Reader
	size  int64
	owner *openCounter
}

func (c *countedSource) Size() int64  { return c.size }
func (c *countedSource) Close() error { return nil }
func (c *countedSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := c.Reader.ReadAt(p, off)
	c.owner.reads.Add(int64(n))
	return n, err
}

// readPage is one page request end to end.
func readPage(t *testing.T, cache *PageCache, key string, o *openCounter, n int) ([]byte, string) {
	t.Helper()
	set, err := OpenComicPages(context.Background(), cache, key, o.open)
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()
	rc, mime, err := set.Page(n)
	if err != nil {
		t.Fatalf("Page(%d): %v", n, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read page %d: %v", n, err)
	}
	return b, mime
}

// The heart of #329: a solid RAR is decoded once, not once per page.
//
// Solid is the shape that makes this matter — every file continues the
// previous one's dictionary, so reaching page n means decoding pages
// 0..n — and the naive container swap behind the old ZIP-only page
// reader would have paid that on every request. The counter is the
// assertion: five page requests, one open of the archive.
func TestComicPagingDecodesASolidRAROnceAcrossManyPages(t *testing.T) {
	pages := []rarEntry{
		{name: "ComicInfo.xml", data: []byte(comicInfoFixtureXML), solid: true},
		{name: "page1.png", data: comicPage("one"), solid: true},
		{name: "page2.png", data: comicPage("two"), solid: true},
		{name: "page3.png", data: comicPage("three"), solid: true},
		{name: "notes.txt", data: []byte("skip"), solid: true},
		{name: "page10.png", data: comicPage("ten"), solid: true},
	}
	o := &openCounter{raw: rarArchive(pages...)}
	cache := NewPageCache(t.TempDir(), 1<<20)

	// Natural sort: page1, page2, page3, page10 — and the two non-image
	// entries are not pages at all.
	want := []string{"one", "two", "three", "ten"}
	for i, label := range want {
		body, mime := readPage(t, cache, "solid", o, i)
		if !bytes.Equal(body, comicPage(label)) {
			t.Errorf("page %d body = %q, want the bytes of the %q page", i, body, label)
		}
		if mime != "image/png" {
			t.Errorf("page %d mime = %q, want image/png", i, mime)
		}
	}
	// One more, out of order, to be sure the answer is not "sequential
	// access happens to work".
	if body, _ := readPage(t, cache, "solid", o, 1); !bytes.Equal(body, comicPage("two")) {
		t.Errorf("re-reading page 1 gave %q", body)
	}

	if got := o.opens.Load(); got != 1 {
		t.Errorf("the archive was opened %d times for %d page requests, want 1",
			got, len(want)+1)
	}
}

// The page count is answered from the same extraction, so opening a
// comic and reading it is one decode rather than two.
func TestComicPagingCountsPagesWithoutASecondDecode(t *testing.T) {
	o := &openCounter{raw: rarArchive(
		rarEntry{name: "01.png", data: comicPage("one"), solid: true},
		rarEntry{name: "02.png", data: comicPage("two"), solid: true},
	)}
	cache := NewPageCache(t.TempDir(), 1<<20)

	set, err := OpenComicPages(context.Background(), cache, "k", o.open)
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	if set.Len() != 2 {
		t.Errorf("Len() = %d, want 2", set.Len())
	}
	_ = set.Close()

	readPage(t, cache, "k", o, 0)
	if got := o.opens.Load(); got != 1 {
		t.Errorf("the archive was opened %d times for a count and a page, want 1", got)
	}
}

// Two readers opening the same .cbr at once must cost one extraction.
// The single-flight is in the cache; this is the seam above it holding
// up under the interleaving that actually happens — two requests for
// page 0 landing together on a cold cache.
func TestComicPagingExtractsOnceForConcurrentReaders(t *testing.T) {
	// Big enough that an extra extraction is unmistakable next to the
	// few bytes each reader spends sniffing the container's magic.
	big := func(label string) []byte {
		return append(append([]byte{}, fakePNG...), pageFiller(label, 256<<10)...)
	}
	o := &openCounter{raw: rarArchive(
		rarEntry{name: "01.png", data: big("one"), solid: true},
		rarEntry{name: "02.png", data: big("two"), solid: true},
	)}
	cache := NewPageCache(t.TempDir(), 4<<20)

	const readers = 8
	var wg sync.WaitGroup
	bodies := make([][]byte, readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			set, err := OpenComicPages(context.Background(), cache, "k", o.open)
			if err != nil {
				t.Errorf("OpenComicPages: %v", err)
				return
			}
			defer func() { _ = set.Close() }()
			rc, _, err := set.Page(1)
			if err != nil {
				t.Errorf("Page: %v", err)
				return
			}
			defer func() { _ = rc.Close() }()
			b, rerr := io.ReadAll(rc)
			if rerr != nil {
				t.Errorf("read: %v", rerr)
				return
			}
			bodies[i] = b
		}()
	}
	wg.Wait()

	for i, b := range bodies {
		if !bytes.Equal(b, big("two")) {
			t.Fatalf("reader %d got %q", i, b)
		}
	}
	// Every reader may *open* the archive — the miss is only settled
	// under the cache's lock — but only one may extract it, which is
	// what the byte counter shows: a second extraction would read the
	// archive a second time.
	if got, size := o.reads.Load(), int64(len(o.raw)); got > size+size/2 {
		t.Errorf("%d readers read %d bytes of a %d byte archive — more than one extraction",
			readers, got, size)
	}
}

// 7z takes the same route and for a reason worth pinning: its random
// access is at the folder level, and a comic's pages are one solid
// folder, so page 400 costs pages 0..399 exactly as a RAR's would.
func TestComicPagingServesA7zComicFromTheCache(t *testing.T) {
	raw, err := os.ReadFile(cb7Fixture)
	if err != nil {
		t.Fatal(err)
	}
	o := &openCounter{raw: raw}
	cache := NewPageCache(t.TempDir(), 1<<20)

	// The fixture holds page10.png and page2.png (plus a ComicInfo and a
	// notes.txt, neither of which is a page): natural sort puts page2
	// first.
	body, mime := readPage(t, cache, "k", o, 0)
	if !bytes.Equal(body, comicPage("page-two")) {
		t.Errorf("page 0 = %q, want the bytes of page2.png", body)
	}
	if mime != "image/png" {
		t.Errorf("page 0 mime = %q, want image/png", mime)
	}
	body, _ = readPage(t, cache, "k", o, 1)
	if !bytes.Equal(body, comicPage("ten")) {
		t.Errorf("page 1 = %q, want the bytes of page10.png", body)
	}
	if got := o.opens.Load(); got != 1 {
		t.Errorf("the archive was opened %d times, want 1", got)
	}

	set, err := OpenComicPages(context.Background(), cache, "k", o.open)
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()
	if set.Len() != 2 {
		t.Errorf("Len() = %d, want 2 pages out of four entries", set.Len())
	}
}

// A page's type comes from its bytes, never from the name the archive
// gave it. The extension chose which entries are pages; it does not get
// to decide what is served back as a Content-Type, the same rule the
// cover pass makes one tier down (#330).
//
// And an entry whose bytes are no image at all is refused with the same
// sentinel the ZIP arm answers — not served as an opaque download. One
// contract for "page n", whatever the container (#334).
func TestComicPagingTypesPagesFromTheirOwnBytes(t *testing.T) {
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0x11}, 32)...)
	o := &openCounter{raw: rarArchive(
		// A JPEG under a .png name, and a page that is not an image at
		// all under a .jpg one.
		rarEntry{name: "01.png", data: jpeg},
		rarEntry{name: "02.jpg", data: []byte("<html><script>alert(1)</script>")},
		rarEntry{name: "03.png", data: comicPage("three")},
	)}
	cache := NewPageCache(t.TempDir(), 1<<20)

	if _, mime := readPage(t, cache, "k", o, 0); mime != "image/jpeg" {
		t.Errorf("page 0 mime = %q, want image/jpeg — the name was believed", mime)
	}

	set, err := OpenComicPages(context.Background(), cache, "k", o.open)
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()
	if _, _, perr := set.Page(1); !errors.Is(perr, ErrComicPageNotImage) {
		t.Errorf("Page(1) err = %v, want ErrComicPageNotImage — the extracted arm served a non-image", perr)
	}
	// The refusal is one slot's, not the comic's: its neighbours still
	// serve, and the bad entry keeps its place in the numbering.
	if set.Len() != 3 {
		t.Errorf("Len() = %d, want 3 — the refused page lost its slot", set.Len())
	}
	rc, mime, perr := set.Page(2)
	if perr != nil {
		t.Fatalf("Page(2): %v", perr)
	}
	defer func() { _ = rc.Close() }()
	if mime != "image/png" {
		t.Errorf("page 2 mime = %q, want image/png", mime)
	}
}

// A page over the per-page cap is dropped without taking its neighbours
// or the page numbering with it: the reader counts pages by position, so
// removing one would silently renumber the rest of the comic.
func TestComicPagingKeepsTheSlotOfAPageItCannotExtract(t *testing.T) {
	huge := make([]byte, comicMaxPageBytes+1)
	copy(huge, fakePNG)
	o := &openCounter{raw: rarArchive(
		rarEntry{name: "01.png", data: comicPage("one")},
		rarEntry{name: "02.png", data: huge},
		rarEntry{name: "03.png", data: comicPage("three")},
	)}
	cache := NewPageCache(t.TempDir(), 1<<30)

	set, err := OpenComicPages(context.Background(), cache, "k", o.open)
	if err != nil {
		t.Fatalf("OpenComicPages: %v", err)
	}
	defer func() { _ = set.Close() }()

	if set.Len() != 3 {
		t.Fatalf("Len() = %d, want 3 — the oversized page lost its slot", set.Len())
	}
	if _, _, err := set.Page(1); err == nil {
		t.Error("the oversized page was served")
	} else if !strings.Contains(err.Error(), "cap") {
		t.Errorf("page 1 error = %v, want it to say why", err)
	}
	for _, n := range []int{0, 2} {
		rc, _, perr := set.Page(n)
		if perr != nil {
			t.Fatalf("page %d: %v", n, perr)
		}
		_ = rc.Close()
	}
}

// A comic with no images is refused rather than served as an empty
// book — the same answer the ingest pass gives the same archive.
func TestComicPagingRefusesAnArchiveWithNoPages(t *testing.T) {
	o := &openCounter{raw: rarArchive(rarEntry{name: "readme.txt", data: []byte("nothing")})}
	cache := NewPageCache(t.TempDir(), 1<<20)

	if set, err := OpenComicPages(context.Background(), cache, "k", o.open); err == nil {
		_ = set.Close()
		t.Fatal("expected an error for an image-less archive")
	} else if !strings.Contains(err.Error(), "no images") {
		t.Errorf("err = %v, want it to say the archive has no images", err)
	}
}

// A comic whose expanded pages would not fit one archive's share of the
// cache is refused with a sentence rather than written out anyway.
//
// The budget is the cache's *per-archive* share, not its whole capacity:
// the capacity is shared with every other comic being read right now, so
// spending all of it on one archive is how N concurrent cold comics put
// N capacities on disk. The cap here is four times the archive budget,
// and this comic sits between the two — it would have been admitted
// under the old rule.
func TestComicPagingRefusesAComicOverOneArchivesBudget(t *testing.T) {
	page := func(label string) []byte {
		return append(append([]byte{}, fakePNG...), pageFiller(label, 400<<10)...)
	}
	o := &openCounter{raw: rarArchive(
		rarEntry{name: "01.png", data: page("one")},
		rarEntry{name: "02.png", data: page("two")},
		rarEntry{name: "03.png", data: page("three")},
	)}
	// 4 MiB cap, so a 1 MiB archive budget; the three pages are ~1.2 MiB.
	cache := NewPageCache(t.TempDir(), 4<<20)

	set, err := OpenComicPages(context.Background(), cache, "k", o.open)
	if err == nil {
		_ = set.Close()
		t.Fatal("a comic over one archive's budget was extracted")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("err = %v, want it to say the comic is over the budget", err)
	}
	// And nothing of it was kept: the failed fill is not remembered, and
	// its bytes are not charged to the cache.
	if cache.bytes != 0 {
		t.Errorf("the refused comic left %d bytes charged to the cache", cache.bytes)
	}
	if _, ok := cache.entries["k"]; ok {
		t.Error("the refused comic was left in the index")
	}
}

// fakeComicWalker is a container that already has its entries in hand,
// so a test can drive the extraction without a real archive.
type fakeComicWalker struct {
	names []string
	data  map[string][]byte
}

func (f fakeComicWalker) entries() []string { return f.names }

func (f fakeComicWalker) stream(_ context.Context, want map[string]bool, sink pageSink) error {
	for _, n := range f.names {
		if !want[n] {
			continue
		}
		if err := sink(n, bytes.NewReader(f.data[n])); err != nil {
			return err
		}
	}
	return nil
}

// The budget is a bound on what is on disk, not on what is discovered to
// have been written. Checking it after each page let every in-flight
// extraction overshoot by a whole page cap — four concurrent fills
// putting an extra 128 MiB between them, against a cap that claimed
// otherwise.
//
// Driven at the extraction directly, because that is the only level at
// which the directory survives the failure and can be weighed.
func TestExtractComicPagesNeverWritesPastTheBudget(t *testing.T) {
	const budget = 300 << 10
	page := func(label string) []byte {
		return append(append([]byte{}, fakePNG...), pageFiller(label, 200<<10)...)
	}
	w := fakeComicWalker{
		names: []string{"01.png", "02.png", "03.png"},
		data: map[string][]byte{
			"01.png": page("one"), "02.png": page("two"), "03.png": page("three"),
		},
	}

	dir := t.TempDir()
	_, _, err := extractComicPages(context.Background(), "cbr", w, dir, budget)
	if err == nil {
		t.Fatal("three pages of 200 KiB fitted a 300 KiB budget")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("err = %v, want the budget error", err)
	}

	// The directory is the evidence: this is what was physically on disk
	// at the moment the extraction gave up.
	var onDisk int64
	ents, rerr := os.ReadDir(dir)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range ents {
		info, ierr := e.Info()
		if ierr != nil {
			t.Fatal(ierr)
		}
		onDisk += info.Size()
	}
	// One byte of slack: the copy reads one past its limit to tell a
	// page landing exactly on the bound from one over it.
	if onDisk > budget+1 {
		t.Errorf("extraction left %d bytes on disk against a %d byte budget", onDisk, budget)
	}
}

// The two bounds a page is written under answer differently, because
// they mean different things: an oversized page is dropped and the comic
// carries on, a spent budget ends the extraction.
func TestWriteCachedPageTellsAnOversizedPageFromASpentBudget(t *testing.T) {
	body := bytes.Repeat([]byte{0x7f}, 1024)

	t.Run("over the page cap", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "p")
		_, _, err := writeCachedPage(path, bytes.NewReader(body), 100, 1<<20)
		if err == nil || errors.Is(err, errPageBudget) {
			t.Fatalf("err = %v, want the page cap error", err)
		}
		if !strings.Contains(err.Error(), "100 byte cap") {
			t.Errorf("err = %v, want it to name the page cap", err)
		}
		if _, serr := os.Stat(path); !errors.Is(serr, os.ErrNotExist) {
			t.Error("a refused page was left on disk")
		}
	})

	t.Run("over the remaining budget", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "p")
		if _, _, err := writeCachedPage(path, bytes.NewReader(body), 1<<20, 100); !errors.Is(err, errPageBudget) {
			t.Fatalf("err = %v, want errPageBudget", err)
		}
	})

	t.Run("no budget left at all", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "p")
		if _, _, err := writeCachedPage(path, bytes.NewReader(body), 1<<20, -5); !errors.Is(err, errPageBudget) {
			t.Fatalf("err = %v, want errPageBudget", err)
		}
	})

	t.Run("within both", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "p")
		png := append(append([]byte{}, fakePNG...), body...)
		n, mime, err := writeCachedPage(path, bytes.NewReader(png), 1<<20, 1<<20)
		if err != nil {
			t.Fatalf("writeCachedPage: %v", err)
		}
		if n != int64(len(png)) || mime != "image/png" {
			t.Errorf("n = %d, mime = %q, want %d and image/png", n, mime, len(png))
		}
	})
}
