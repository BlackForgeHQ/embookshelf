// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// fillerOfSize returns a page filler that writes one file of n bytes and
// counts how many times it ran. The counter is the whole point of most
// of these tests: the cache's contract is that a key is filled once.
func fillerOfSize(t *testing.T, n int64, calls *atomic.Int64) pageExtractor {
	t.Helper()
	return func(_ context.Context, dir string) ([]cachedPage, int64, error) {
		calls.Add(1)
		name := "0"
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o600); err != nil {
			return nil, 0, err
		}
		return []cachedPage{{name: "page.png", file: name, mime: "image/png", size: n}}, n, nil
	}
}

// Two readers opening the same comic must cost one extraction, not two.
// This is the property the whole cache exists for — a solid RAR decodes
// its whole dictionary to reach a page, so a second concurrent reader
// paying that again is the bug, not a slow path.
func TestPageCacheFillsAKeyOnceUnderConcurrency(t *testing.T) {
	c := NewPageCache(t.TempDir(), 1<<20)
	var calls atomic.Int64

	// The filler blocks until every goroutine has arrived, so the window
	// a second caller could squeeze a second fill into is held open for
	// the whole test rather than left to scheduling luck.
	start := make(chan struct{})
	fill := func(ctx context.Context, dir string) ([]cachedPage, int64, error) {
		calls.Add(1)
		<-start
		return fillerOfSize(t, 8, &atomic.Int64{})(ctx, dir)
	}

	const readers = 16
	dirs := make([]string, readers)
	var wg, arrived sync.WaitGroup
	arrived.Add(readers)
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Signalled immediately before the call, so the fill is
			// still blocked when the last goroutine enters acquire.
			arrived.Done()
			e, err := c.acquire(context.Background(), "k", fill)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			dirs[i] = e.dir
			c.release(e)
		}()
	}
	arrived.Wait()
	// Every goroutine is inside (or about to enter) acquire, and the one
	// that won is parked in the filler. Only now let it finish.
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("filler ran %d times, want 1", got)
	}
	for i, d := range dirs {
		if d == "" || d != dirs[0] {
			t.Fatalf("reader %d got dir %q, want every reader on %q", i, d, dirs[0])
		}
	}
}

// The cache is bounded. Admitting past the cap evicts least-recently
// used entries and takes their bytes off the disk, not just out of the
// map — a page cache that only forgot would fill the data root.
func TestPageCacheEvictsLeastRecentlyUsedPastTheCap(t *testing.T) {
	c := NewPageCache(t.TempDir(), 250)
	// The reservation a fill is admitted against. Set to what these
	// fillers actually write, so the test is about eviction order rather
	// than about the default quarter-of-the-cap estimate.
	c.archiveBudget = 100
	var calls atomic.Int64

	dirs := map[string]string{}
	for _, k := range []string{"a", "b", "c"} {
		e, err := c.acquire(context.Background(), k, fillerOfSize(t, 100, &calls))
		if err != nil {
			t.Fatalf("acquire %s: %v", k, err)
		}
		dirs[k] = e.dir
		c.release(e)
	}

	if c.bytes > 250 {
		t.Errorf("cache holds %d bytes, over the 250 byte cap", c.bytes)
	}
	if _, ok := c.entries["a"]; ok {
		t.Errorf("the least recently used entry is still in the cache")
	}
	if _, err := os.Stat(dirs["a"]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("evicted entry's directory still on disk: %v", err)
	}
	for _, k := range []string{"b", "c"} {
		if _, err := os.Stat(dirs[k]); err != nil {
			t.Errorf("live entry %s lost its directory: %v", k, err)
		}
	}
}

// Eviction must not delete pages out from under a request that is
// streaming them. A referenced entry is passed over — the cap is a
// budget the cache spends down when it can, not an invariant it enforces
// against a response in flight — and becomes evictable the moment its
// last reader lets go.
func TestPageCacheDoesNotEvictAnEntryStillBeingRead(t *testing.T) {
	c := NewPageCache(t.TempDir(), 250)
	c.archiveBudget = 100
	var calls atomic.Int64

	held, err := c.acquire(context.Background(), "a", fillerOfSize(t, 100, &calls))
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	heldDir := held.dir

	admit := func(k string) {
		t.Helper()
		e, err := c.acquire(context.Background(), k, fillerOfSize(t, 100, &calls))
		if err != nil {
			t.Fatalf("acquire %s: %v", k, err)
		}
		c.release(e)
	}
	// Three more entries at 100 bytes each against a 250 byte cap: the
	// oldest is "a", and it is the one the cap wants first.
	for _, k := range []string{"b", "c", "d"} {
		admit(k)
	}

	if _, ok := c.entries["a"]; !ok {
		t.Fatalf("the entry being read was evicted")
	}
	if _, err := os.Stat(filepath.Join(heldDir, "0")); err != nil {
		t.Fatalf("the held entry's page vanished while it was being read: %v", err)
	}

	// Let go, and it is no longer protected.
	c.release(held)
	admit("e")
	if _, ok := c.entries["a"]; ok {
		t.Errorf("the released entry survived the next admission")
	}
	if _, err := os.Stat(heldDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the evicted entry's directory is still on disk: %v", err)
	}
}

// Publication is atomic: a half-written extraction is never visible
// under the key's own name, so a reader that finds the directory finds
// every page in it. Enforced by building elsewhere and renaming.
func TestPageCachePublishesAtomically(t *testing.T) {
	root := t.TempDir()
	c := NewPageCache(root, 1<<20)

	final := filepath.Join(root, pageCacheDirName("k"))
	fill := func(_ context.Context, dir string) ([]cachedPage, int64, error) {
		if dir == final {
			t.Errorf("filler wrote straight into the published directory %q", dir)
		}
		if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the published directory exists mid-fill: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0"), []byte("page"), 0o600); err != nil {
			return nil, 0, err
		}
		return []cachedPage{{file: "0", size: 4}}, 4, nil
	}

	e, err := c.acquire(context.Background(), "k", fill)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer c.release(e)
	if e.dir != final {
		t.Errorf("entry dir = %q, want the published %q", e.dir, final)
	}
	if _, err := os.Stat(filepath.Join(final, "0")); err != nil {
		t.Errorf("published directory is missing its page: %v", err)
	}
	// Nothing left behind: the temp directory the fill ran in is gone.
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("root holds %d entries, want only the published one", len(ents))
	}
}

// A failed extraction is not remembered as a failure. The archive may
// have been mid-upload or the disk briefly full; the next reader gets a
// fresh attempt rather than a cached refusal.
func TestPageCacheDoesNotRememberAFailedFill(t *testing.T) {
	root := t.TempDir()
	c := NewPageCache(root, 1<<20)
	boom := errors.New("extract: disk full")

	var calls atomic.Int64
	fail := func(_ context.Context, dir string) ([]cachedPage, int64, error) {
		calls.Add(1)
		// Half a comic on disk, then a failure — the case that must not
		// be published under the key.
		if err := os.WriteFile(filepath.Join(dir, "0"), []byte("half"), 0o600); err != nil {
			return nil, 0, err
		}
		return nil, 0, boom
	}

	for range 2 {
		if _, err := c.acquire(context.Background(), "k", fail); !errors.Is(err, boom) {
			t.Fatalf("acquire err = %v, want the filler's error", err)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("filler ran %d times, want 2 — the failure was cached", got)
	}
	if c.bytes != 0 {
		t.Errorf("cache accounts %d bytes for a failed fill", c.bytes)
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("root holds %d entries after two failed fills, want none", len(ents))
	}
}

// An unkeyed acquire is private: nothing is shared and nothing is kept.
// This is the answer for a book whose bytes have no stable identity to
// key on — correct, just not amortised.
func TestPageCacheUnkeyedEntriesArePrivateAndDiscarded(t *testing.T) {
	root := t.TempDir()
	c := NewPageCache(root, 1<<20)
	var calls atomic.Int64

	first, err := c.acquire(context.Background(), "", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	second, err := c.acquire(context.Background(), "", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if first.dir == second.dir {
		t.Errorf("two unkeyed acquires shared directory %q", first.dir)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("filler ran %d times, want 2 — an unkeyed entry was shared", got)
	}
	c.release(first)
	c.release(second)
	for _, d := range []string{first.dir, second.dir} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("private directory %q survived release: %v", d, err)
		}
	}
	if len(c.entries) != 0 {
		t.Errorf("private entries entered the shared index")
	}
}

// A cache that was never wired still serves. An install or a test that
// constructs a handler without one must page comics, unshared, rather
// than fail.
func TestPageCacheNilReceiverFillsPrivately(t *testing.T) {
	var c *PageCache
	var calls atomic.Int64

	e, err := c.acquire(context.Background(), "k", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire on a nil cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "0")); err != nil {
		t.Fatalf("nil cache produced no page: %v", err)
	}
	c.release(e)
	if _, err := os.Stat(e.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("nil cache leaked %q", e.dir)
	}
}

// tryAcquire is the lookup that lets a warm comic answer without ever
// opening the archive's bytes — the read an object-store library would
// otherwise pay per page.
func TestPageCacheTryAcquireOnlyAnswersForKnownKeys(t *testing.T) {
	c := NewPageCache(t.TempDir(), 1<<20)
	var calls atomic.Int64

	if e, err := c.tryAcquire(context.Background(), "k"); e != nil || err != nil {
		t.Fatalf("tryAcquire on a cold cache = (%v, %v), want (nil, nil)", e, err)
	}
	e, err := c.acquire(context.Background(), "k", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	c.release(e)

	warm, err := c.tryAcquire(context.Background(), "k")
	if err != nil || warm == nil {
		t.Fatalf("tryAcquire on a warm cache = (%v, %v), want the entry", warm, err)
	}
	c.release(warm)
	if got := calls.Load(); got != 1 {
		t.Errorf("filler ran %d times, want 1", got)
	}
}

// The root is wiped on first use: page directories from a process that
// is gone describe archives nothing has re-verified, and the in-memory
// index that named them died with it.
func TestPageCacheWipesStaleDirectoriesOnFirstUse(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "e-"+strconv.Itoa(1))
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewPageCache(root, 1<<20)
	var calls atomic.Int64
	e, err := c.acquire(context.Background(), "k", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer c.release(e)
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a previous process's page directory survived: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Review fixes
// ---------------------------------------------------------------------------

// A fill is shared work, so the requester who started it does not get to
// cancel it. Before this, the first reader closing their tab mid-
// extraction aborted the walk, dropped the entry and answered every
// waiter with context.Canceled — and since the comics worth caching are
// exactly the ones whose first reader gives up waiting, a cache could be
// kept permanently cold by the very requests that would have warmed it.
func TestPageCacheFillOutlivesTheRequesterThatStartedIt(t *testing.T) {
	c := NewPageCache(t.TempDir(), 1<<20)

	entered := make(chan struct{})
	finish := make(chan struct{})
	var fillErr error
	fill := func(ctx context.Context, dir string) ([]cachedPage, int64, error) {
		close(entered)
		<-finish
		// The requester is already gone by now. If the fill were running
		// under their context this would be context.Canceled, which is
		// what the extraction walks check between entries.
		// Exactly what the extraction walks do between entries: give up
		// when the context is done. That is the mechanism by which a
		// requester's cancel used to destroy everyone else's fill.
		fillErr = ctx.Err()
		if fillErr != nil {
			return nil, 0, fmt.Errorf("extract: %w", fillErr)
		}
		if err := os.WriteFile(filepath.Join(dir, "0"), []byte("page"), 0o600); err != nil {
			return nil, 0, err
		}
		return []cachedPage{{file: "0", size: 4}}, 4, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	winner := make(chan error, 1)
	go func() {
		e, err := c.acquire(ctx, "k", fill)
		c.release(e)
		winner <- err
	}()
	<-entered

	waiter := make(chan *cacheEntry, 1)
	waiterErr := make(chan error, 1)
	go func() {
		e, err := c.acquire(context.Background(), "k", fill)
		waiter <- e
		waiterErr <- err
	}()

	// The requester gives up while the extraction is parked.
	cancel()
	close(finish)

	if err := <-waiterErr; err != nil {
		t.Fatalf("the waiter got %v — the requester's cancel took the fill with it", err)
	}
	e := <-waiter
	if e == nil {
		t.Fatal("the waiter got no entry")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "0")); err != nil {
		t.Errorf("the published entry has no page: %v", err)
	}
	c.release(e)
	<-winner

	if fillErr != nil {
		t.Errorf("the fill ran under a cancelled context (%v)", fillErr)
	}
	// And it is in the cache, so the next reader is warm.
	if _, ok := c.entries["k"]; !ok {
		t.Error("the entry was not published")
	}
}

// The unlink of an evicted entry happens after the lock is released, so
// a re-fill of the same key can be publishing while it is still pending.
// The victim is therefore renamed out of its published name *under the
// lock*: without that, the stalled unlink deletes the fresh entry's
// directory and leaves the index pointing at nothing.
//
// Driven directly rather than through timing, so the interleaving is the
// test rather than something the test hopes for.
func TestPageCachePendingUnlinkCannotDeleteARepublishedKey(t *testing.T) {
	root := t.TempDir()
	c := NewPageCache(root, 1<<20)
	var calls atomic.Int64

	first, err := c.acquire(context.Background(), "k", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	published := first.dir
	c.release(first)

	// Evict it, and stall before unlinking — the window the bug lived in.
	c.mu.Lock()
	trash := c.dropLocked(c.entries["k"])
	c.mu.Unlock()

	if len(trash) != 1 {
		t.Fatalf("dropLocked returned %d directories, want 1", len(trash))
	}
	if trash[0] == published {
		t.Errorf("the victim is still under its published name %q — a re-fill would publish onto it", published)
	}
	if _, serr := os.Stat(published); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("the published name is still taken while the unlink is pending: %v", serr)
	}

	// Same key, filled again, while the unlink above has not run yet.
	second, err := c.acquire(context.Background(), "k", fillerOfSize(t, 8, &calls))
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer c.release(second)
	if second.dir != published {
		t.Fatalf("the re-fill published to %q, want the canonical %q", second.dir, published)
	}

	// Now the stalled unlink lands.
	removeAll(trash)

	if _, serr := os.Stat(filepath.Join(second.dir, "0")); serr != nil {
		t.Errorf("the pending unlink deleted the freshly published entry: %v", serr)
	}
}

// The cap is a cap, not an average. Every fill is charged its archive
// budget for as long as it runs, and a comic that cannot be admitted is
// refused — because the alternative, warning and carrying on, meant N
// concurrent cold comics could each put a whole cache's worth on disk.
func TestPageCacheRefusesAComicItCannotFitRatherThanExceedingTheCap(t *testing.T) {
	root := t.TempDir()
	c := NewPageCache(root, 200)
	c.archiveBudget = 150

	entered := make(chan struct{})
	finish := make(chan struct{})
	slow := func(ctx context.Context, dir string) ([]cachedPage, int64, error) {
		close(entered)
		<-finish
		return fillerOfSize(t, 150, &atomic.Int64{})(ctx, dir)
	}

	firstDone := make(chan error, 1)
	var first *cacheEntry
	go func() {
		e, err := c.acquire(context.Background(), "a", slow)
		first = e
		firstDone <- err
	}()
	<-entered

	// "a" holds a 150-byte reservation against a 200-byte cap and cannot
	// be evicted (it is being filled), so there is no room for "b".
	var calls atomic.Int64
	if _, err := c.acquire(context.Background(), "b", fillerOfSize(t, 150, &calls)); !errors.Is(err, ErrPageCacheFull) {
		t.Errorf("second comic err = %v, want ErrPageCacheFull", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("the refused comic was extracted anyway (%d fills)", got)
	}

	close(finish)
	if err := <-firstDone; err != nil {
		t.Fatalf("first comic: %v", err)
	}
	if c.bytes > c.maxBytes {
		t.Errorf("cache holds %d bytes over a %d byte cap", c.bytes, c.maxBytes)
	}

	// Once the first reader lets go, the second comic fits.
	c.release(first)
	second, err := c.acquire(context.Background(), "b", fillerOfSize(t, 150, &calls))
	if err != nil {
		t.Fatalf("second comic after release: %v", err)
	}
	defer c.release(second)
	if c.bytes > c.maxBytes {
		t.Errorf("cache holds %d bytes over a %d byte cap", c.bytes, c.maxBytes)
	}
}

// The per-archive budget is well under the whole cap, so the reservation
// that keeps the cap honest cannot itself be the cap.
func TestPageCacheArchiveBudgetIsWellUnderTheCap(t *testing.T) {
	c := NewPageCache(t.TempDir(), DefaultPageCacheBytes)
	if c.archiveBudget >= c.maxBytes/2 || c.archiveBudget <= 0 {
		t.Errorf("archiveBudget = %d against a %d byte cap", c.archiveBudget, c.maxBytes)
	}
}
