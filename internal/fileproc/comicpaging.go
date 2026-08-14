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
	"os"
	"path"
	"path/filepath"
	"strconv"

	"github.com/blackforge/embookshelf/internal/storage"
)

// The reader's paging seam, for all three comic containers (#329).
//
// #310 gave .cbr and .cb7 an ingest path, so a RAR- or 7z-packed comic
// reaches the shelf with a cover and metadata, but the page endpoints
// stayed ZIP-only and answered 415: neither container serves a random
// page for the price a ZIP does, and swapping the container behind the
// page reader would have made every page cost a decode of everything
// before it. What was missing was not a container, it was a place to put
// the pages once they had been decoded. PageCache is that place, and
// this file is the dispatch above it:
//
//	ZIP        served straight out of the archive, unchanged — one
//	           range read of one entry, nothing expanded or cached
//	RAR, 7z    extracted once into the page cache, served from there
//
// Both of the cached containers are here for the same reason and it is
// not "RAR is sequential". 7z *is* random access at the archive level —
// the header at the tail names every entry — but its unit of compression
// is the folder, and 7-Zip's default packs a comic's pages into one
// solid folder, so opening page 400 decodes pages 0..399 to get there.
// sevenzip amortises that within one *sevenzip.Reader via a folder
// reader pool, which is exactly the thing a request-scoped reader over a
// request-scoped Source does not get to keep. So the two containers make
// the same shape of mistake and take the same cure.

// ErrComicContainer is what the page reader answers for a comic whose
// bytes are none of the three containers it knows. It exists because all
// three comic extensions stamp books.format = CBZ (model.FormatSpecs),
// so anything on the shelf as a comic opens the comic reader, and the
// reader UI can say "this file is not a comic archive" where it cannot
// usefully say anything about a zip parser's complaint.
//
// Reserved for bytes that match no container's magic. A damaged archive
// of a container we do know keeps its own error — that is a different
// sentence and a different status, and telling the owner of a broken
// .cbz that it "isn't a comic archive" would send them looking for a
// conversion they do not need.
var ErrComicContainer = errors.New(
	"not a comic archive: pages can be served from .cbz, .cbr and .cb7")

// SourceOpener hands back the comic's bytes. Deferred rather than an
// already-open Source because a warm cache answers without any: on an
// object-store library that is the difference between a file read and a
// network round trip for every page after the first.
type SourceOpener func() (storage.Source, error)

// comicContainer is which archiver packed a comic.
type comicContainer int

const (
	containerUnknown comicContainer = iota
	containerZIP
	containerRAR
	container7z
)

// Container magic, read from the front of the file rather than from any
// tail structure: the question here is what the file *is*, and a
// truncated archive has lost its tail — which is exactly the case that
// must not be mistaken for a different container.
//
// ZIP has two openings: a first local file header, or the
// end-of-central-directory record of an archive with no entries. (The
// third, PK\x07\x08, only heads a spanned archive's first segment, which
// is not a comic anyone ships.) RAR has two: RAR4 and RAR5.
var containerMagic = []struct {
	magic []byte
	kind  comicContainer
}{
	{magic: []byte{'P', 'K', 0x03, 0x04}, kind: containerZIP},
	{magic: []byte{'P', 'K', 0x05, 0x06}, kind: containerZIP},
	{magic: []byte("Rar!\x1a\x07\x00"), kind: containerRAR},
	{magic: []byte("Rar!\x1a\x07\x01\x00"), kind: containerRAR},
	{magic: []byte("7z\xbc\xaf\x27\x1c"), kind: container7z},
}

func sniffComicContainer(src storage.Source) comicContainer {
	var head [8]byte
	n, err := src.ReadAt(head[:], 0)
	if n == 0 && err != nil {
		return containerUnknown
	}
	for _, c := range containerMagic {
		if len(c.magic) <= n && bytes.Equal(head[:len(c.magic)], c.magic) {
			return c.kind
		}
	}
	return containerUnknown
}

// ComicPageSet is one opened comic's pages, ready to serve by number.
// Callers must Close it: it owns either an open Source (the ZIP arm) or
// a reference on a page cache entry (the extracted arm), and the second
// is what stops eviction from deleting a page mid-response.
type ComicPageSet struct {
	// ZIP arm: the archive stays open and a page is one entry out of it.
	src   storage.Source
	zr    *zip.Reader
	names []string

	// Extracted arm: the pages are files in a cache entry's directory.
	cache *PageCache
	entry *cacheEntry
}

// OpenComicPages resolves a comic to its pages, extracting it into the
// cache first when its container cannot serve a numbered page cheaply.
//
// key identifies the *bytes*, not the book: a replaced file must not be
// served its predecessor's pages, so the caller passes a content hash
// where it has one. An empty key (or a nil cache) still works — the
// extraction is done privately and thrown away, which is slower but
// never wrong.
func OpenComicPages(
	ctx context.Context, cache *PageCache, key string, open SourceOpener,
) (*ComicPageSet, error) {
	// The warm path first, so a comic already extracted costs no read of
	// the archive at all.
	if e, err := cache.tryAcquire(ctx, key); err != nil {
		return nil, err
	} else if e != nil {
		return &ComicPageSet{cache: cache, entry: e}, nil
	}

	src, err := open()
	if err != nil {
		return nil, err
	}
	kind := sniffComicContainer(src)

	if kind == containerZIP {
		zr, zerr := zip.NewReader(src, src.Size())
		if zerr != nil {
			_ = src.Close()
			return nil, fmt.Errorf("open cbz: %w", zerr)
		}
		return &ComicPageSet{
			src:   src,
			zr:    zr,
			names: comicPages((&zipComic{zr: zr}).entries()),
		}, nil
	}

	// Everything below extracts, so the archive's bytes are wanted only
	// for as long as the extraction runs — and not at all when another
	// caller's extraction of the same key wins the race.
	defer func() { _ = src.Close() }()

	// What one comic may expand to. The cache's per-archive budget, not
	// its whole capacity: the capacity is shared with every other comic
	// being read right now, and a budget equal to it would mean N
	// concurrent cold comics could put N capacities on disk.
	budget := DefaultPageCacheBytes / 4
	if cache != nil {
		budget = cache.archiveBudget
	}
	var fill pageExtractor
	switch kind {
	case containerRAR:
		fill = func(ctx context.Context, dir string) ([]cachedPage, int64, error) {
			a, aerr := newRARComic(src)
			if aerr != nil {
				return nil, 0, aerr
			}
			return extractComicPages(ctx, "cbr", a, dir, budget)
		}
	case container7z:
		fill = func(ctx context.Context, dir string) ([]cachedPage, int64, error) {
			a, aerr := newSevenzipComic(src)
			if aerr != nil {
				return nil, 0, aerr
			}
			return extractComicPages(ctx, "cb7", a, dir, budget)
		}
	default:
		return nil, ErrComicContainer
	}

	e, err := cache.acquire(ctx, key, fill)
	if err != nil {
		return nil, err
	}
	return &ComicPageSet{cache: cache, entry: e}, nil
}

// Len is the page count, in reading order.
func (p *ComicPageSet) Len() int {
	if p == nil {
		return 0
	}
	if p.entry != nil {
		return len(p.entry.pages)
	}
	return len(p.names)
}

// TypedFromBytes reports where this set's page types come from, which
// decides when the caller may set Content-Type.
//
// True for the extracted containers: their pages are on disk, so each
// one is typed from its own leading bytes (SniffImageMime) and the type
// is known before a byte of the body is written. False for ZIP, which
// streams the entry without ever holding it — there is nothing to sniff
// in time, the type comes from the entry's name, and the caller leaves
// the header unset so net/http types the response from the body it sees.
// That is what has always decided a .cbz page's content type; #331 is
// about the name-derived answer this leaves in place for the one case
// net/http cannot help with, an entry with no bytes at all.
func (p *ComicPageSet) TypedFromBytes() bool { return p != nil && p.entry != nil }

// Page opens page n (0-indexed, natural sort order) for reading, with
// the MIME type TypedFromBytes describes.
func (p *ComicPageSet) Page(n int) (io.ReadCloser, string, error) {
	if p == nil || n < 0 || n >= p.Len() {
		return nil, "", fmt.Errorf("page %d out of range (0..%d)", n, p.Len()-1)
	}
	if p.entry != nil {
		pg := p.entry.pages[n]
		if pg.file == "" {
			return nil, "", fmt.Errorf("page %d (%s) could not be extracted: %s", n, pg.name, pg.fail)
		}
		f, err := os.Open(filepath.Join(p.entry.dir, pg.file))
		if err != nil {
			return nil, "", fmt.Errorf("open page %d: %w", n, err)
		}
		return f, pg.mime, nil
	}

	name := p.names[n]
	for _, f := range p.zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", fmt.Errorf("open page: %w", err)
		}
		return rc, mimeFromExt(path.Ext(name)), nil
	}
	return nil, "", fmt.Errorf("page %d entry %q vanished", n, name)
}

// Close releases whatever the set was holding.
func (p *ComicPageSet) Close() error {
	if p == nil {
		return nil
	}
	if p.entry != nil {
		p.cache.release(p.entry)
		p.entry = nil
		return nil
	}
	if p.src != nil {
		src := p.src
		p.src = nil
		return src.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// Extraction
// ---------------------------------------------------------------------------

// pageSink receives one entry's bytes during an extraction walk. It
// returns an error only for a failure that makes the rest of the walk
// meaningless — a page that will not decode is recorded and stepped
// over, the same degradation a bad cover entry gets at ingest.
type pageSink func(name string, r io.Reader) error

// comicPageWalker is a container that can hand every wanted entry's
// bytes to a sink in one pass over the archive.
//
// One pass, not one call per entry, for the same reason comicArchive
// takes its wanted set all at once: the containers that need this are
// the ones where a second pass costs a second decode.
type comicPageWalker interface {
	entries() []string
	stream(ctx context.Context, want map[string]bool, sink pageSink) error
}

// extractComicPages writes every page of a comic into dir and returns
// the reading order.
//
// A page that will not extract keeps its slot in the order and loses
// only its file: the reader numbers pages by position, so dropping one
// would silently renumber every page after it, and one bad entry is not
// worth turning the rest of the comic into a different book.
func extractComicPages(
	ctx context.Context, kind string, a comicPageWalker, dir string, budget int64,
) ([]cachedPage, int64, error) {
	names := comicPages(a.entries())
	if len(names) == 0 {
		return nil, 0, fmt.Errorf("%s contains no images", kind)
	}

	pages := make([]cachedPage, len(names))
	// first maps an entry name to the slot that owns its file. Archives
	// can name two entries the same; the first wins, as it does for the
	// cover at ingest, and the later slots point at the same bytes.
	first := make(map[string]int, len(names))
	want := make(map[string]bool, len(names))
	for i, n := range names {
		pages[i] = cachedPage{name: n, fail: "the archive did not yield this entry"}
		if _, seen := first[n]; !seen {
			first[n] = i
			want[n] = true
		}
	}

	var total int64
	err := a.stream(ctx, want, func(name string, r io.Reader) error {
		i, ok := first[name]
		if !ok {
			return nil
		}
		file := strconv.Itoa(i)
		// Two bounds, and the copy stops at the tighter one. Checking
		// the budget *after* writing would let each in-flight extraction
		// overshoot by a whole page — four concurrent fills putting an
		// extra 128 MiB on disk between them, which is the cap not
		// meaning what it says.
		n, mime, werr := writeCachedPage(filepath.Join(dir, file), r, comicMaxPageBytes, budget-total)
		if errors.Is(werr, errPageBudget) {
			return fmt.Errorf("%s expands past the %d byte page cache budget", kind, budget)
		}
		if werr != nil {
			pages[i].fail = werr.Error()
			slog.Warn("comic page unreadable, dropped",
				"container", kind, "entry", name, "err", werr)
			return nil
		}
		total += n
		pages[i].file, pages[i].mime, pages[i].size, pages[i].fail = file, mime, n, ""
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	for i, n := range names {
		if owner := first[n]; owner != i {
			pages[i].file, pages[i].mime, pages[i].fail = pages[owner].file, pages[owner].mime, pages[owner].fail
		}
	}
	return pages, total, nil
}

// errPageBudget says a page was stopped because the comic had used up
// its share of the cache, not because the page itself was too big. The
// two are different answers: one page is dropped and the comic goes on,
// a spent budget ends the extraction.
var errPageBudget = errors.New("the comic's page cache budget is spent")

// writeCachedPage copies one page to disk and types it from its own
// first bytes, refusing one that runs past either bound it is given:
// pageMax, which is what a single page may be, and budgetLeft, which is
// what the whole comic has left.
//
// Both are on the copy rather than on any declared size, because a
// declared size is a number the archive chose: this is what stands
// between a decompression bomb and the data root. Stopping at the
// tighter of the two is also what keeps the cache's cap physical rather
// than notional — nothing is written that the budget has not already
// been checked against.
func writeCachedPage(
	path string, r io.Reader, pageMax, budgetLeft int64,
) (n int64, mime string, err error) {
	max, spent := pageMax, false
	if budgetLeft < max {
		max, spent = budgetLeft, true
	}
	if max < 0 {
		max = 0
	}

	f, err := os.Create(path)
	if err != nil {
		return 0, "", err
	}
	defer func() {
		cerr := f.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			_ = os.Remove(path)
		}
	}()

	// One byte past the cap, so a page landing exactly on it is not
	// mistaken for one over it.
	lr := io.LimitReader(r, max+1)

	// Enough of the front to sniff, held back so the type is known
	// before the caller ever asks for it.
	var head [12]byte
	hn, herr := io.ReadFull(lr, head[:])
	if herr != nil && !errors.Is(herr, io.EOF) && !errors.Is(herr, io.ErrUnexpectedEOF) {
		return 0, "", herr
	}
	mime = SniffImageMime(head[:hn])
	if mime == "" {
		// The entry's name said it was an image; its bytes disagree, and
		// the bytes decide. Served as an opaque download rather than as
		// whatever type the name claimed.
		mime = "application/octet-stream"
	}
	if _, werr := f.Write(head[:hn]); werr != nil {
		return 0, "", werr
	}
	rest, cerr := io.Copy(f, lr)
	if cerr != nil {
		return 0, "", cerr
	}
	n = int64(hn) + rest
	if n > max {
		if spent {
			return 0, "", errPageBudget
		}
		return 0, "", fmt.Errorf("page expands past the %d byte cap", pageMax)
	}
	return n, mime, nil
}
