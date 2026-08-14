// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// The comic page cache: extract a comic's pages once, serve them many
// times (#329).
//
// It exists for exactly one reason. A ZIP answers "give me page 400"
// with a range read of that entry — the central directory says where it
// starts and its deflate stream stands alone — so the reader has always
// served .cbz straight out of the archive with nothing expanded. RAR and
// 7z do not work that way. A solid RAR's page 400 continues the
// dictionary of the 399 pages before it, and a 7z folder is one
// compressed stream that has to be decoded from its start; either way,
// reaching page n means decoding pages 0..n. Serving a 200-page comic
// that way is quadratic in the archive, and over an object-store library
// it is also 200 downloads of the same object. So the first request pays
// one decode and every page after it is a file read.
//
// What is *not* here is deliberate. No database table: a page cache is
// disposable, and a row surviving the bytes it describes is a bug
// waiting to happen. No background sweeper: the cap is enforced on
// admission, so the only thing that can grow the cache is a reader, and
// the reader is what shrinks it. No configuration: the size is a
// constant and the location comes from DATA_PATH, which is already the
// answer for covers and audiobook staging.
//
// Keyed by content hash (the caller's choice — see the handler), so a
// replaced or deleted book cannot be served its predecessor's pages: new
// bytes are a new key, and the old key ages out of the cap.

// DefaultPageCacheBytes is how much disk the extracted pages of all
// comics currently being read may occupy.
//
// One gibibyte is several manga volumes' worth, which is the working set
// of a person reading, and it is the same order as the decode budget a
// single solid archive is already allowed to spend at ingest
// (cbrMaxSolidDrainBytes). It also bounds the cost of the worst case:
// one archive whose pages do not fit is extracted, served, and evicted
// on the next admission rather than filling the data root.
const DefaultPageCacheBytes int64 = 1 << 30

// comicMaxPageBytes bounds a single extracted page. Same number as the
// cover cap and for the same reason: a page is one scan, and anything
// past this is a declared size the archive chose rather than an image
// anybody drew. A page over the cap is dropped from the extraction and
// the *index keeps its slot*, so the pages around it still answer and
// the reader's numbering does not shift under it.
const comicMaxPageBytes int64 = comicMaxCoverBytes

// ErrPageCacheFull is the answer when a comic cannot be admitted without
// pushing the cache past its cap and there is nothing evictable left —
// every other entry is being read right now.
//
// A refusal rather than an overrun, because the alternative is worse
// than a failed request: N readers each opening a different cold comic
// would each be allowed their own archive budget, and the "cap" would
// describe nothing. Transient by construction — the readers holding the
// cache down finish — so it is a retry, and the handler says so with a
// 503.
var ErrPageCacheFull = errors.New("the comic page cache is full; try again shortly")

// PageCache is the extract-once page store. The zero value is not
// usable; NewPageCache builds one. A nil *PageCache is usable and means
// "no sharing": every acquire extracts privately and throws the result
// away, which is what an install with no data root gets.
type PageCache struct {
	root     string
	maxBytes int64
	// archiveBudget is the most one comic's extracted pages may occupy,
	// and the amount reserved up front for a fill that has not finished
	// yet. Well below maxBytes on purpose: it is what makes the cap hold
	// under concurrency, because a reservation is what the next caller
	// is admitted against.
	archiveBudget int64

	mu sync.Mutex
	// ready is whether root has been created (and wiped, once).
	ready bool
	// entries indexes live entries by key. An entry is in here from the
	// moment its filler starts, which is what makes the fill single —
	// the second caller finds it and waits.
	entries map[string]*cacheEntry
	// lru orders entries most-recently-acquired first. Values are
	// *cacheEntry.
	lru *list.List
	// bytes is the sum of the sizes of the entries in the index, counting
	// a fill in flight at its reservation. Never above maxBytes: that is
	// the invariant admitLocked keeps.
	bytes int64
	// trashSeq names the directories waiting to be unlinked. Unique
	// because a victim is renamed out of its published name while the
	// lock is still held — see dropLocked.
	trashSeq uint64
}

// cacheEntry is one comic's extracted pages.
type cacheEntry struct {
	key string
	// done closes when dir/pages/err are final. Readers that found the
	// entry mid-fill wait on it.
	done chan struct{}
	dir  string
	// pages is the reading order: index i is page i, whatever the
	// archive called it.
	pages []cachedPage
	size  int64
	err   error

	// elem is this entry's place in the LRU list, nil once evicted.
	elem *list.Element
	// refs counts callers currently reading the entry, which is what
	// keeps eviction off it: a referenced entry is never a victim, so a
	// page cannot be deleted mid-stream.
	refs int
	// dead is set when the entry has left the index.
	dead bool
	// unkeyed marks an entry that was never shared: released means gone.
	unkeyed bool
}

// cachedPage is one extracted page on disk.
type cachedPage struct {
	// name is the archive entry this came from, for logs and errors.
	name string
	// file is the base name inside the entry's directory. Empty when the
	// page could not be extracted — the slot survives so the numbering
	// does, and asking for it answers fail.
	file string
	// mime is the type sniffed from the page's own leading bytes, never
	// from the entry name (SniffImageMime). Empty when the bytes are not
	// an image this recognises.
	mime string
	size int64
	// fail says why this page has no file, when it has none.
	fail string
}

// pageExtractor extracts a comic's pages into dir and describes them. It
// returns the pages in reading order and the total bytes written; dir is
// a private directory that is either published whole or removed whole.
type pageExtractor func(ctx context.Context, dir string) ([]cachedPage, int64, error)

// NewPageCache builds a cache under root, holding at most maxBytes of
// extracted pages. root is created on first use and wiped then: the
// index that named the directories in it is in memory, so anything found
// there belongs to a process that is gone.
func NewPageCache(root string, maxBytes int64) *PageCache {
	if maxBytes <= 0 {
		maxBytes = DefaultPageCacheBytes
	}
	return &PageCache{
		root:     root,
		maxBytes: maxBytes,
		// A quarter of the cap. The number itself is arbitrary; what is
		// not is that it be well below maxBytes, because it is charged
		// to the cache for the whole length of a fill and four
		// simultaneous cold comics are a reader opening four tabs, not
		// an attack. At the default that is 256 MiB per comic, which is
		// above any real volume's expanded pages.
		archiveBudget: maxBytes / 4,
		entries:       make(map[string]*cacheEntry),
		lru:           list.New(),
	}
}

// pageCacheDirName is the on-disk name for a key. Hashed rather than
// used verbatim because a key is whatever identity the caller had — a
// content hash usually, a location otherwise — and a directory name has
// to be a directory name.
func pageCacheDirName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "e-" + hex.EncodeToString(sum[:])
}

// tryAcquire returns a referenced entry when key is already in the
// cache, waiting for an in-flight fill, and (nil, nil) when it is not.
//
// Separate from acquire because the miss must not be paid for: this is
// the lookup that lets a warm comic answer page 400 without opening the
// archive at all, which on an object-store library is the difference
// between a file read and a network round trip per page.
func (c *PageCache) tryAcquire(ctx context.Context, key string) (*cacheEntry, error) {
	if c == nil || key == "" {
		return nil, nil
	}
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return nil, nil
	}
	c.hold(e)
	c.mu.Unlock()
	return c.wait(ctx, e)
}

// acquire returns a referenced entry for key, filling it with fill when
// this caller is the one that inserted it. A caller that loses the race
// waits for the winner's fill and never runs its own.
//
// An empty key (or a nil cache) is an unshared extraction: filled into a
// private directory, removed on release. That is the honest answer for a
// book whose bytes have no stable identity to key on — correct, just not
// amortised — rather than a shared entry keyed on something that cannot
// tell one file's bytes from another's.
func (c *PageCache) acquire(ctx context.Context, key string, fill pageExtractor) (*cacheEntry, error) {
	// A cache with no root has nowhere to publish to, so it shares
	// nothing — the same answer an absent cache and an unkeyed comic
	// get, and the reason the three are one branch.
	if c == nil || c.root == "" || key == "" {
		return c.fillPrivate(ctx, fill)
	}

	c.mu.Lock()
	if err := c.initLocked(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if e, ok := c.entries[key]; ok {
		c.hold(e)
		c.mu.Unlock()
		return c.wait(ctx, e)
	}
	// Room for the whole archive budget before a byte is written, so
	// that the caller after this one is admitted against a fill that has
	// not landed yet rather than against a stale zero.
	trash, ok := c.admitLocked(c.archiveBudget)
	if !ok {
		c.mu.Unlock()
		removeAll(trash)
		return nil, ErrPageCacheFull
	}
	e := &cacheEntry{key: key, done: make(chan struct{}), refs: 1, size: c.archiveBudget}
	c.bytes += c.archiveBudget
	c.entries[key] = e
	e.elem = c.lru.PushFront(e)
	c.mu.Unlock()
	removeAll(trash)

	fillCtx, endFill := fillContext(ctx)
	dir, pages, size, err := c.publish(fillCtx, key, fill)
	endFill()

	c.mu.Lock()
	e.dir, e.pages, e.err = dir, pages, err
	// The reservation stops being an estimate. It can only shrink — the
	// extractor is bounded by the same budget — so nothing needs
	// evicting to make the result fit; the guard below is for a filler
	// that ignored its budget, which is a test, not a container.
	c.bytes += size - e.size
	e.size = size
	close(e.done)
	if err != nil {
		// Not remembered as a failure: the archive may have been
		// mid-upload, or the disk briefly full, and the next reader
		// deserves a fresh attempt rather than a cached refusal.
		trash = c.dropLocked(e)
	} else {
		trash, _ = c.admitLocked(0)
	}
	c.mu.Unlock()
	removeAll(trash)

	if err != nil {
		c.release(e)
		return nil, err
	}
	return e, nil
}

// pageExtractTimeout bounds a fill that has been cut loose from its
// requester. Generous: it covers downloading and expanding a gibibyte of
// comic over an object store, and exists only so a wedged backend cannot
// hold a reservation forever.
const pageExtractTimeout = 15 * time.Minute

// fillContext is the context an extraction runs under.
//
// Deliberately not the requester's. A fill is shared work: every waiter
// on the key is depending on it, and the first requester closing their
// tab must not cancel the extraction out from under them — which it did,
// aborting the walk, dropping the entry and answering every waiter with
// context.Canceled. Worse, it meant a comic whose first reader tends to
// give up (a big archive, a slow backend — exactly the case the cache is
// for) could never warm at all, because each attempt cancelled the one
// that would have fixed it.
//
// WithoutCancel rather than context.Background so the request's values —
// logging and tracing scope — survive into the work it started. Waiters
// still wait under their own context and give up whenever they like; it
// is only the fill that is no longer theirs to stop.
func fillContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), pageExtractTimeout)
}

// hold takes a reference and marks the entry most recently used. Called
// with the lock held.
func (c *PageCache) hold(e *cacheEntry) {
	e.refs++
	if e.elem != nil {
		c.lru.MoveToFront(e.elem)
	}
}

// wait blocks until the entry's filler has finished, and hands back the
// reference it was given — or drops it, if the fill failed or the caller
// gave up first.
func (c *PageCache) wait(ctx context.Context, e *cacheEntry) (*cacheEntry, error) {
	select {
	case <-e.done:
	case <-ctx.Done():
		c.release(e)
		return nil, ctx.Err()
	}
	if e.err != nil {
		c.release(e)
		return nil, e.err
	}
	return e, nil
}

// publish runs the filler in a private directory and renames it into
// place. Nothing is ever written under the key's own name: a reader that
// finds that directory finds a complete extraction, because it appeared
// in one step.
func (c *PageCache) publish(
	ctx context.Context, key string, fill pageExtractor,
) (dir string, pages []cachedPage, size int64, err error) {
	tmp, err := os.MkdirTemp(c.root, ".fill-")
	if err != nil {
		return "", nil, 0, fmt.Errorf("comic page cache: stage: %w", err)
	}
	pages, size, err = fill(ctx, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, 0, err
	}
	final := filepath.Join(c.root, pageCacheDirName(key))
	// A directory already at the destination is a fill this process does
	// not know about, which the wipe on first use should have removed.
	// Clearing it is cheaper than failing the read over it.
	_ = os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, 0, fmt.Errorf("comic page cache: publish: %w", err)
	}
	return final, pages, size, nil
}

// fillPrivate extracts into a directory nobody else can find, for the
// unkeyed case. It never enters the index, so it is neither shared nor
// counted against the cap — its whole lifetime is one caller's.
func (c *PageCache) fillPrivate(ctx context.Context, fill pageExtractor) (*cacheEntry, error) {
	root := ""
	if c != nil {
		c.mu.Lock()
		err := c.initLocked()
		root = c.root
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	dir, err := os.MkdirTemp(root, ".private-")
	if err != nil {
		return nil, fmt.Errorf("comic page cache: stage: %w", err)
	}
	pages, size, err := fill(ctx, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	e := &cacheEntry{done: make(chan struct{}), dir: dir, pages: pages, size: size, refs: 1, unkeyed: true}
	close(e.done)
	return e, nil
}

// release drops one reference.
//
// It never unlinks a shared entry, and that is not an omission: a shared
// entry's bytes go away in dropLocked, which can only run when nothing
// holds a reference, so by the time the last release happens there is
// nothing left to clean up. Dropping the reference is what makes the
// entry evictable at all.
//
// An unkeyed entry has exactly one owner and no index to leave, so
// release is where it goes.
func (c *PageCache) release(e *cacheEntry) {
	if e == nil {
		return
	}
	if e.unkeyed || c == nil {
		if e.dir != "" {
			_ = os.RemoveAll(e.dir)
			e.dir = ""
		}
		return
	}
	c.mu.Lock()
	e.refs--
	c.mu.Unlock()
}

// admitLocked makes room for want more bytes, returning the directories
// to unlink and whether the room was found.
//
// Entries with readers are never victims — a page cannot be deleted out
// from under a response that is streaming it — so "no room" is a real
// state and not a warning to sail past: with every entry referenced, the
// only way to honour the cap is to refuse. want is 0 when the caller is
// simply settling up after a fill, which cannot fail.
func (c *PageCache) admitLocked(want int64) ([]string, bool) {
	var trash []string
	for c.bytes+want > c.maxBytes {
		victim := c.oldestEvictableLocked()
		if victim == nil {
			if want > 0 {
				slog.Warn("comic page cache full, refusing a comic",
					"bytes", c.bytes, "wantBytes", want,
					"capBytes", c.maxBytes, "entries", len(c.entries))
			}
			return trash, false
		}
		trash = append(trash, c.dropLocked(victim)...)
	}
	return trash, true
}

func (c *PageCache) oldestEvictableLocked() *cacheEntry {
	for el := c.lru.Back(); el != nil; el = el.Prev() {
		e, _ := el.Value.(*cacheEntry)
		if e == nil || e.refs > 0 {
			continue
		}
		return e
	}
	return nil
}

// dropLocked takes an entry out of the index and returns its directory
// for unlinking.
//
// The directory is *renamed* out of its published name here, while the
// lock is still held, rather than handed over under that name for the
// caller to unlink after unlocking. That gap is a real bug: between
// dropping the entry and unlinking it, another goroutine can re-fill the
// same key, and publish would RemoveAll+Rename onto the very path the
// stalled unlink is about to delete — leaving a live index entry
// pointing at nothing and every page 500ing until it was evicted again.
// Renaming under the lock means the published name is free the instant
// the entry leaves the index, so no unlink can ever race a publish.
//
// Only ever called for an entry nobody is reading: the evictor skips
// referenced entries, and the other caller is a failed fill, which has
// no directory to leave behind.
func (c *PageCache) dropLocked(e *cacheEntry) []string {
	if e.dead {
		return nil
	}
	e.dead = true
	if cur, ok := c.entries[e.key]; ok && cur == e {
		delete(c.entries, e.key)
	}
	if e.elem != nil {
		c.lru.Remove(e.elem)
		e.elem = nil
	}
	c.bytes -= e.size
	e.size = 0
	if e.dir == "" {
		return nil
	}
	dir := e.dir
	e.dir = ""
	c.trashSeq++
	trash := filepath.Join(c.root, fmt.Sprintf(".trash-%d", c.trashSeq))
	if err := os.Rename(dir, trash); err != nil {
		// The rename is the safety, not the unlink, so a failure here
		// is worth saying out loud — and unlinking the original name is
		// still better than leaking it.
		slog.Warn("comic page cache could not stage an evicted entry for removal",
			"dir", dir, "err", err)
		return []string{dir}
	}
	return []string{trash}
}

// initLocked creates the cache root, wiping whatever a previous process
// left in it. Called with the lock held.
func (c *PageCache) initLocked() error {
	if c.ready {
		return nil
	}
	if c.root == "" {
		// No root configured: MkdirTemp("") lands in the OS temp
		// directory, which is where an unkeyed fill belongs anyway.
		c.ready = true
		return nil
	}
	if err := os.RemoveAll(c.root); err != nil {
		return fmt.Errorf("comic page cache: clear %s: %w", c.root, err)
	}
	if err := os.MkdirAll(c.root, 0o755); err != nil {
		return fmt.Errorf("comic page cache: create %s: %w", c.root, err)
	}
	c.ready = true
	return nil
}

func removeAll(dirs []string) {
	for _, d := range dirs {
		_ = os.RemoveAll(d)
	}
}
