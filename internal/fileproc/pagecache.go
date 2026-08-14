// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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

// PageCache is the extract-once page store. The zero value is not
// usable; NewPageCache builds one. A nil *PageCache is usable and means
// "no sharing": every acquire extracts privately and throws the result
// away, which is what an install with no data root gets.
type PageCache struct {
	root     string
	maxBytes int64

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
	// bytes is the sum of the sizes of the entries in the index.
	bytes int64
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
	// refs counts callers currently reading the entry. Eviction takes an
	// entry out of the index immediately but leaves its bytes until the
	// last reader lets go, so a page cannot be deleted mid-stream.
	refs int
	// dead is set when the entry has left the index. Its directory is
	// removed on the release that drops refs to zero.
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
		entries:  make(map[string]*cacheEntry),
		lru:      list.New(),
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
	if c == nil || key == "" {
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
	e := &cacheEntry{key: key, done: make(chan struct{}), refs: 1}
	c.entries[key] = e
	e.elem = c.lru.PushFront(e)
	c.mu.Unlock()

	dir, pages, size, err := c.publish(ctx, key, fill)

	c.mu.Lock()
	e.dir, e.pages, e.err = dir, pages, err
	if err == nil {
		e.size = size
		c.bytes += size
	}
	close(e.done)
	var trash []string
	if err != nil {
		// Not remembered as a failure: the archive may have been
		// mid-upload, or the disk briefly full, and the next reader
		// deserves a fresh attempt rather than a cached refusal.
		trash = c.dropLocked(e)
	} else {
		trash = c.evictLocked()
	}
	c.mu.Unlock()
	removeAll(trash)

	if err != nil {
		c.release(e)
		return nil, err
	}
	return e, nil
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

// release drops one reference. The last release of an entry that has
// left the index — evicted, failed, or private — is what removes its
// bytes, so a page can never be deleted out from under a response that
// is still streaming it.
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
	var trash string
	if e.dead && e.refs <= 0 && e.dir != "" {
		trash, e.dir = e.dir, ""
	}
	c.mu.Unlock()
	if trash != "" {
		_ = os.RemoveAll(trash)
	}
}

// evictLocked brings the cache back under its cap, returning the
// directories to remove. Entries with readers are skipped rather than
// waited for: the cap is a budget, not an invariant to enforce against
// a request in flight.
func (c *PageCache) evictLocked() []string {
	var trash []string
	for c.bytes > c.maxBytes {
		victim := c.oldestEvictableLocked()
		if victim == nil {
			slog.Warn("comic page cache over its cap with nothing evictable",
				"bytes", c.bytes, "capBytes", c.maxBytes, "entries", len(c.entries))
			break
		}
		trash = append(trash, c.dropLocked(victim)...)
	}
	return trash
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
// for removal when nobody is reading it.
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
	if e.refs > 0 || e.dir == "" {
		return nil
	}
	dir := e.dir
	e.dir = ""
	return []string{dir}
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
