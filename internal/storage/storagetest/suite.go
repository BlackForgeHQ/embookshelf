// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storagetest provides a contract test suite that any
// storage.Storage implementation must pass. Backends call RunSuite (or
// RunArm, for a backend production roots above the keyspace under test)
// from their own test packages with a factory that returns a fresh
// backend rooted at a clean state.
package storagetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// MakeBackend returns a fresh, empty Storage. The cleanup func is
// called by the suite when each subtest finishes.
type MakeBackend func(t *testing.T) (storage.Storage, func())

// RootShape declares what an Arm's backend is rooted at, relative to the
// keys the suite writes.
//
// It is not a detail of test setup. The root decides which key shapes the
// backend is ever handed, and a backend that joined the suite without
// saying which shape it is would be graded against a keyspace no
// deployment gives it — which is exactly what happened to LocalFS, tested
// only over a t.TempDir() root that production never builds (ADR-0030 §1).
type RootShape int

const (
	// RootUndeclared is the zero value and is not a shape. RunArm fails an
	// Arm that leaves Root unset rather than assuming the common case: a
	// silent default is how the asymmetry above went unnoticed, and a
	// future backend must not be able to inherit it by omission.
	RootUndeclared RootShape = iota

	// RootScoped means the backend's own root is the keyspace the suite
	// writes into: it is built fresh per subtest over scratch space it
	// owns, and every key the suite passes is relative to that root. The
	// S3 backend with a per-subtest prefix and a LocalFS built over
	// t.TempDir() are both this shape.
	RootScoped

	// RootGlobal means the backend is rooted above the keyspace the suite
	// writes into, and one instance serves the whole install. LocalFS is
	// this shape in every deployment: storageloader roots it at "/"
	// (ADR-0030 §1), so its keys are whole filesystem paths and it is
	// handed two shapes of them — absolute, from the shim that joins a
	// library-relative files.location onto the library root, and
	// root-relative without the leading slash, which is what List hands
	// back and what an unshimmed caller passes.
	//
	// An Arm of this shape supplies a scratch Subtree, and the suite runs
	// the whole contract underneath it once per key shape.
	RootGlobal
)

func (r RootShape) String() string {
	switch r {
	case RootScoped:
		return "RootScoped"
	case RootGlobal:
		return "RootGlobal"
	default:
		return "RootUndeclared"
	}
}

// Instance is one freshly built backend, as returned by Arm.New.
type Instance struct {
	// Storage is the backend under test.
	Storage storage.Storage

	// Subtree is the absolute filesystem path of the scratch space the
	// suite may write into. Required for RootGlobal — the suite addresses
	// every key underneath it, which is what stops a "/"-rooted backend
	// writing to the real filesystem root — and must be empty for
	// RootScoped, where the backend's own root already is the scratch
	// space.
	Subtree string

	// Close is called when each subtest finishes. May be nil.
	Close func()
}

func (i Instance) close() {
	if i.Close != nil {
		i.Close()
	}
}

// Arm is one member of the suite: a factory, plus the declaration of what
// the backend it builds is rooted at.
type Arm struct {
	Root RootShape
	New  func(t *testing.T) Instance
}

// RunSuite runs every contract test against a backend rooted at scratch
// space it owns — the RootScoped shorthand for RunArm. Backend authors
// call:
//
//	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
//	    fs, _ := local.New(t.TempDir())
//	    return fs, func() {}
//	})
//
// A backend production roots above the keys it is handed declares that
// instead, with RunArm and RootGlobal.
func RunSuite(t *testing.T, make MakeBackend) {
	t.Helper()
	RunArm(t, Arm{
		Root: RootScoped,
		New: func(t *testing.T) Instance {
			s, cleanup := make(t)
			return Instance{Storage: s, Close: cleanup}
		},
	})
}

// RunArm runs the contract against one declared arm.
//
// For RootScoped that is the contract once, against the backend as built.
// For RootGlobal it is the contract once per key shape the deployed
// backend receives, with every key addressed under the arm's scratch
// Subtree, plus the cross-shape check that the two shapes name the same
// object.
func RunArm(t *testing.T, arm Arm) {
	t.Helper()
	if arm.New == nil {
		t.Fatal("storagetest: Arm.New is nil")
	}
	switch arm.Root {
	case RootScoped:
		runContract(t, func(t *testing.T) (storage.Storage, func()) {
			inst := arm.New(t)
			if inst.Subtree != "" {
				t.Fatalf("storagetest: RootScoped arm supplied Subtree %q; a "+
					"scoped backend is already rooted at its own scratch space",
					inst.Subtree)
			}
			return inst.Storage, inst.close
		})
	case RootGlobal:
		for _, shape := range keyShapes {
			t.Run(shape.name, func(t *testing.T) {
				runContract(t, func(t *testing.T) (storage.Storage, func()) {
					inst := arm.New(t)
					r := rebased{
						inner: inst.Storage,
						base:  shape.base(t, inst.Subtree),
					}
					// The wrapper must answer a PrefixMover assertion the
					// same way the backend it wraps does: a method set is
					// static in Go, so the extension rides a second type.
					if _, ok := inst.Storage.(storage.PrefixMover); ok {
						return rebasedMover{r}, inst.close
					}
					return r, inst.close
				})
			})
		}
		t.Run("KeyShapesNameTheSameObject", func(t *testing.T) {
			testKeyShapesNameTheSameObject(t, arm)
		})
	default:
		t.Fatalf("storagetest: Arm.Root is %v — an arm must declare what its "+
			"backend is rooted at, because that decides which key shapes the "+
			"contract is verified against (ADR-0030 §1)", arm.Root)
	}
}

// keyShapes are the two forms a RootGlobal backend is handed the same
// path in. Both must name the same object; the suite runs the whole
// contract in each.
var keyShapes = []struct {
	name string
	base func(t *testing.T, subtree string) string
}{
	// What service.LibraryHandle.StorageKey produces, and what the scan
	// worker and bookdrop ingest pass: a whole filesystem path.
	{"AbsoluteKeys", func(t *testing.T, subtree string) string {
		return requireSubtree(t, subtree)
	}},
	// What List hands back from a "/"-rooted backend, and what a caller
	// that skipped the shim passes: the same path with no leading slash.
	{"RootRelativeKeys", func(t *testing.T, subtree string) string {
		return strings.TrimPrefix(requireSubtree(t, subtree), "/")
	}},
}

func requireSubtree(t *testing.T, subtree string) string {
	t.Helper()
	if subtree == "" {
		t.Fatal("storagetest: RootGlobal arm supplied no Subtree; the suite " +
			"needs scratch space to address keys under, or a \"/\"-rooted " +
			"backend would write to the real filesystem root")
	}
	if !filepath.IsAbs(subtree) {
		t.Fatalf("storagetest: RootGlobal Subtree %q is not absolute", subtree)
	}
	return strings.TrimSuffix(filepath.ToSlash(filepath.Clean(subtree)), "/")
}

// rebased addresses a backend rooted above the suite's keyspace. Every
// key the suite passes is joined onto base on the way out, and every key
// the backend reports is stripped back on the way in, so the contract
// tests stay written in plain keys while the backend sees the shape a
// deployment gives it.
type rebased struct {
	inner storage.Storage
	base  string
}

func (r rebased) out(key string) string {
	if key == "" {
		return r.base
	}
	return r.base + "/" + strings.TrimPrefix(key, "/")
}

// in maps a key the backend reported back onto the suite's logical key.
//
// It compares with the leading slash off both sides on purpose. A
// "/"-rooted backend answers to "/a/b" but reports "a/b" from List,
// because a listing key is relative to the root and that root is "/".
// That asymmetry is the thing this arm exists to run the contract over,
// not something in here to paper over: a key that does not sit under the
// subtree at all comes back untouched, and the assertion that receives it
// fails.
func (r rebased) in(key string) string {
	k := strings.TrimPrefix(key, "/")
	b := strings.TrimPrefix(r.base, "/")
	switch {
	case k == b:
		return ""
	case strings.HasPrefix(k, b+"/"):
		return k[len(b)+1:]
	default:
		return key
	}
}

func (r rebased) inAll(keys []string) []string {
	if keys == nil {
		return nil
	}
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = r.in(k)
	}
	return out
}

func (r rebased) Capabilities() storage.Capability { return r.inner.Capabilities() }

func (r rebased) List(ctx context.Context, prefix string) (storage.Iterator, error) {
	it, err := r.inner.List(ctx, r.out(prefix))
	if err != nil {
		return nil, err
	}
	return rebasedIter{inner: it, r: r}, nil
}

func (r rebased) Head(ctx context.Context, key string) (storage.ObjectInfo, error) {
	info, err := r.inner.Head(ctx, r.out(key))
	info.Key = r.in(info.Key)
	return info, err
}

func (r rebased) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return r.inner.Get(ctx, r.out(key))
}

func (r rebased) Put(ctx context.Context, key string, body io.Reader, opts ...storage.PutOption) (storage.PutResult, error) {
	return r.inner.Put(ctx, r.out(key), body, opts...)
}

func (r rebased) Delete(ctx context.Context, key string) error {
	return r.inner.Delete(ctx, r.out(key))
}

func (r rebased) MovePrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	return r.inner.MovePrefix(ctx, r.out(oldPrefix), r.out(newPrefix))
}

// rebasedMover carries the PrefixMover extension for a wrapped backend
// that has it. Its own type, not a method on rebased, because a method
// set is static: rebased with the method would advertise the extension
// for every backend, LocalFS included.
type rebasedMover struct{ rebased }

func (r rebasedMover) MovePrefixDetailed(ctx context.Context, oldPrefix, newPrefix string) (storage.MoveResult, error) {
	res, err := r.inner.(storage.PrefixMover).MovePrefixDetailed(ctx, r.out(oldPrefix), r.out(newPrefix))
	res.Written = r.inAll(res.Written)
	res.Reclaim = r.inAll(res.Reclaim)
	return res, err
}

func (r rebased) Open(ctx context.Context, key string) (storage.Source, error) {
	return r.inner.Open(ctx, r.out(key))
}

type rebasedIter struct {
	inner storage.Iterator
	r     rebased
}

func (it rebasedIter) Next(ctx context.Context) (storage.ObjectInfo, error) {
	o, err := it.inner.Next(ctx)
	if err != nil {
		return o, err
	}
	o.Key = it.r.in(o.Key)
	return o, nil
}

func (it rebasedIter) Close() error { return it.inner.Close() }

// testKeyShapesNameTheSameObject pins, for a RootGlobal backend, the one
// invariant the per-shape runs above cannot state on their own: the two
// shapes are not two keyspaces. A path written absolute is readable
// root-relative and the reverse, and a key the backend reports from List
// is a key it accepts back — which is the shape mismatch that has bitten
// this codebase repeatedly (#168, #201, #202, ADR-0030 §2).
func testKeyShapesNameTheSameObject(t *testing.T, arm Arm) {
	inst := arm.New(t)
	defer inst.close()
	s := inst.Storage
	base := requireSubtree(t, inst.Subtree)
	ctx := context.Background()

	abs := base + "/shapes/book.epub"
	rel := strings.TrimPrefix(abs, "/")

	if _, err := s.Put(ctx, abs, strings.NewReader("epub bytes")); err != nil {
		t.Fatalf("Put(%q): %v", abs, err)
	}
	if got := mustRead(t, s, rel); got != "epub bytes" {
		t.Errorf("written as %q, read back as %q = %q, want %q", abs, rel, got, "epub bytes")
	}
	if _, err := s.Head(ctx, rel); err != nil {
		t.Errorf("Head(%q) = %v after Put(%q); the leading slash must not "+
			"make it a different object", rel, err, abs)
	}

	absSidecar := base + "/shapes/metadata.json"
	relSidecar := strings.TrimPrefix(absSidecar, "/")
	if _, err := s.Put(ctx, relSidecar, strings.NewReader("{}")); err != nil {
		t.Fatalf("Put(%q): %v", relSidecar, err)
	}
	if got := mustRead(t, s, absSidecar); got != "{}" {
		t.Errorf("written as %q, read back as %q = %q, want %q", relSidecar, absSidecar, got, "{}")
	}

	// Whatever shape List reports in, that key must address the object.
	// A caller has nothing else to go on.
	it, err := s.List(ctx, base+"/shapes")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	defer func() { _ = it.Close() }()
	listed := 0
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("List.Next: %v", err)
		}
		listed++
		rc, err := s.Get(ctx, o.Key)
		if err != nil {
			t.Errorf("List reported %q but Get on it = %v; a listing key the "+
				"backend will not take back is unusable to every caller", o.Key, err)
			continue
		}
		_ = rc.Close()
	}
	if listed != 2 {
		t.Errorf("List under %q yielded %d objects, want 2", base+"/shapes", listed)
	}
}

// runContract is the contract itself, run against one already-addressed
// factory. RunArm decides what that addressing is.
func runContract(t *testing.T, make MakeBackend) {
	t.Helper()
	t.Run("PutThenGet", func(t *testing.T) { testPutThenGet(t, make) })
	t.Run("HeadReturnsSize", func(t *testing.T) { testHeadReturnsSize(t, make) })
	t.Run("GetMissingNotFound", func(t *testing.T) { testGetMissingNotFound(t, make) })
	t.Run("DeleteRemovesObject", func(t *testing.T) { testDeleteRemovesObject(t, make) })
	t.Run("DeleteMissingIsNoError", func(t *testing.T) { testDeleteMissingIsNoError(t, make) })
	t.Run("MovePrefixRelocatesEveryKey", func(t *testing.T) { testMovePrefixRelocatesEveryKey(t, make) })
	t.Run("MovePrefixMissingIsNotFound", func(t *testing.T) { testMovePrefixMissingIsNotFound(t, make) })
	t.Run("ListYieldsAllKeys", func(t *testing.T) { testListYieldsAllKeys(t, make) })
	t.Run("ListPrefixFilters", func(t *testing.T) { testListPrefixFilters(t, make) })
	t.Run("ListEmptyOnMissingPrefix", func(t *testing.T) { testListEmptyOnMissingPrefix(t, make) })
	t.Run("CapabilitiesIsStable", func(t *testing.T) { testCapabilitiesIsStable(t, make) })
	t.Run("PutDoesNotBufferTheWholeObject", func(t *testing.T) { testPutDoesNotBufferTheWholeObject(t, make) })
	t.Run("OpenReadsBytesAtRandomOffsets", func(t *testing.T) { testOpenRandomAccess(t, make) })
}

func testPutThenGet(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := s.Put(ctx, "k", strings.NewReader("v")); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "v" {
		t.Fatalf("got %q, want %q", got, "v")
	}
}

func testHeadReturnsSize(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "k", bytes.NewReader([]byte("hello")))
	info, err := s.Head(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 5 {
		t.Errorf("Size = %d, want 5", info.Size)
	}
}

func testGetMissingNotFound(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	_, err := s.Get(context.Background(), "missing")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func testDeleteRemovesObject(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "k", strings.NewReader("x"))
	if err := s.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Head(ctx, "k"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after Delete, Head = %v, want ErrNotFound", err)
	}
}

func testDeleteMissingIsNoError(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	if err := s.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

// movePrefixFixture is the multi-key tree the MovePrefix tests move.
// Deliberately more than one key and more than one level: a rename has
// to carry the book, its sidecar and anything nested alongside them.
var movePrefixFixture = map[string]string{
	"old/book.epub":             "epub bytes",
	"old/metadata.json":         "{}",
	"old/extras/cover.jpg":      "jpeg bytes",
	"old/extras/deep/notes.txt": "notes",
}

func putFixture(t *testing.T, s storage.Storage, files map[string]string) {
	t.Helper()
	ctx := context.Background()
	for k, v := range files {
		if _, err := s.Put(ctx, k, strings.NewReader(v)); err != nil {
			t.Fatalf("seed %q: %v", k, err)
		}
	}
}

func mustRead(t *testing.T, s storage.Storage, key string) string {
	t.Helper()
	rc, err := s.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(b)
}

// testMovePrefixRelocatesEveryKey pins the shared half of the MovePrefix
// contract and, for the half the backends legitimately disagree on, the
// disjunction rather than one side of it.
//
// Every key that was under oldPrefix is readable at the corresponding
// newPrefix key with its bytes intact — that part is the same
// everywhere. What happens to the source is not: LocalFS renames the
// directory so the sources are gone, S3 has no rename so they are still
// there and come back in Reclaim for the caller to delete on its own
// schedule. The assertion is therefore "gone XOR listed in Reclaim",
// which both satisfy and neither can satisfy by accident.
func testMovePrefixRelocatesEveryKey(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	putFixture(t, s, movePrefixFixture)

	var res storage.MoveResult
	var err error
	if mover, ok := s.(storage.PrefixMover); ok {
		res, err = mover.MovePrefixDetailed(ctx, "old", "new/home")
	} else {
		err = s.MovePrefix(ctx, "old", "new/home")
	}
	if err != nil {
		t.Fatalf("MovePrefix: %v", err)
	}

	for src, want := range movePrefixFixture {
		dst := "new/home/" + strings.TrimPrefix(src, "old/")
		if got := mustRead(t, s, dst); got != want {
			t.Errorf("after move, %q = %q, want %q", dst, got, want)
		}
	}

	reclaim := map[string]bool{}
	for _, k := range res.Reclaim {
		reclaim[k] = true
	}
	for src := range movePrefixFixture {
		_, err := s.Head(ctx, src)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			if reclaim[src] {
				t.Errorf("%q is gone yet listed in Reclaim — Reclaim is for "+
					"sources that survived the move", src)
			}
		case err != nil:
			t.Errorf("Head(%q): %v", src, err)
		default:
			if !reclaim[src] {
				t.Errorf("%q survived the move but is not in Reclaim — the "+
					"caller has no way to learn it must be reaped", src)
			}
		}
	}

	// Everything Written must actually be there; a caller reclaims this
	// list on failure and a phantom key would mask a real leak.
	for _, k := range res.Written {
		if _, err := s.Head(ctx, k); err != nil {
			t.Errorf("Written lists %q but Head says %v", k, err)
		}
	}
}

// testMovePrefixMissingIsNotFound pins the empty-source case: an error
// the caller can recognise, and no half-built destination.
func testMovePrefixMissingIsNotFound(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	// A neighbour that must not be swept in, and proof the backend is
	// not merely empty.
	if _, err := s.Put(ctx, "elsewhere/keep.txt", strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}

	var res storage.MoveResult
	var err error
	if mover, ok := s.(storage.PrefixMover); ok {
		res, err = mover.MovePrefixDetailed(ctx, "nope", "new")
	} else {
		err = s.MovePrefix(ctx, "nope", "new")
	}
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("MovePrefix on a missing prefix = %v, want ErrNotFound", err)
	}
	if len(res.Written) != 0 || len(res.Reclaim) != 0 {
		t.Errorf("MovePrefix on a missing prefix reported Written=%v Reclaim=%v; "+
			"want nothing written", res.Written, res.Reclaim)
	}
	if _, err := s.Head(ctx, "new"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Head(new) = %v after a failed move, want ErrNotFound", err)
	}
	if got := mustRead(t, s, "elsewhere/keep.txt"); got != "keep" {
		t.Errorf("unrelated key disturbed: %q", got)
	}
}

func testListYieldsAllKeys(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	for _, k := range []string{"a", "b/c", "b/d/e"} {
		_, _ = s.Put(ctx, k, strings.NewReader(""))
	}
	it, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = it.Close() }()
	seen := map[string]bool{}
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[o.Key] = true
	}
	for _, k := range []string{"a", "b/c", "b/d/e"} {
		if !seen[k] {
			t.Errorf("missing %q in list", k)
		}
	}
}

func testListPrefixFilters(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "a/1", strings.NewReader(""))
	_, _ = s.Put(ctx, "a/2", strings.NewReader(""))
	_, _ = s.Put(ctx, "b/3", strings.NewReader(""))
	// Checked, not swallowed: this used to be `it, _ :=`, so a backend
	// that failed the List nil-panicked the whole test binary here and
	// took every later subtest's result with it.
	it, err := s.List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = it.Close() }()
	count := 0
	for {
		_, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != 2 {
		t.Errorf("got %d, want 2", count)
	}
}

func testListEmptyOnMissingPrefix(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	it, err := s.List(context.Background(), "nope")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = it.Close() }()
	if _, err := it.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func testCapabilitiesIsStable(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	c1 := s.Capabilities()
	c2 := s.Capabilities()
	if c1 != c2 {
		t.Fatalf("Capabilities() unstable: %v vs %v", c1, c2)
	}
}

// The two sizes testPutDoesNotBufferTheWholeObject compares. The small
// one is past any plausible adapter's internal chunk — the S3 backend's
// part size is 8 MiB — so a streaming backend has already reached its
// steady-state working set by then and the step up to the large one adds
// nothing to it. Quadrupling is the point: "allocation tracks the object"
// and "allocation is bounded" cannot both survive it.
const (
	streamSmallObject = 16 << 20 // 16 MiB
	streamLargeObject = 64 << 20 // 64 MiB
)

// testPutDoesNotBufferTheWholeObject is the suite-level expression of the
// streaming expectation (ADR-0030 §3, #266, #270): the memory a Put costs
// is the backend's own working set, not the object.
//
// Written as a size — "it works at 500 MB" — this case would have passed
// against the buffering S3 put that shipped, which is exactly how that
// defect survived review: every in-memory fake reads the body into a map,
// so a large-object test proves the machine had the RAM and nothing else.
// So the assertion is on allocation, taken across the call with a reader
// that materialises nothing, which leaves the backend as the only thing
// that can have allocated what the delta counts.
//
// Both backends can express it and both must: LocalFS io.Copy's into a
// temp file, the S3 adapter streams part by part. A backend that cannot
// pass this cannot serve a 5 GB audiobook, whatever its other rows say.
func testPutDoesNotBufferTheWholeObject(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()

	put := func(key string, size int64) uint64 {
		t.Helper()
		var err error
		allocated := allocatedDuring(func() {
			_, err = s.Put(ctx, key, newPattern(size))
		})
		if err != nil {
			t.Fatalf("Put(%q, %s): %v", key, mib(size), err)
		}
		return allocated
	}

	const smallKey, largeKey = "stream/small.bin", "stream/large.bin"
	smallAlloc := put(smallKey, streamSmallObject)
	largeAlloc := put(largeKey, streamLargeObject)

	t.Logf("put of %s allocated %s; put of %s allocated %s",
		mib(streamSmallObject), mib(int64(smallAlloc)),
		mib(streamLargeObject), mib(int64(largeAlloc)))

	// The growth check is the one that needs no calibration against any
	// backend's internals: the slack is a quarter of the *small* object,
	// so an adapter whose cost tracks the object misses by an order of
	// magnitude while a per-chunk overhead that grew fourfold passes.
	const slack = streamSmallObject / 4
	if largeAlloc > smallAlloc+slack {
		t.Errorf("quadrupling the object from %s to %s took allocation from %s "+
			"to %s, a rise of %s (slack %s). Put's cost tracks the object, so "+
			"the backend is holding the body rather than streaming it.",
			mib(streamSmallObject), mib(streamLargeObject),
			mib(int64(smallAlloc)), mib(int64(largeAlloc)),
			mib(int64(largeAlloc-smallAlloc)), mib(slack))
	}
	// And an absolute ceiling, because a backend that buffered *both*
	// objects would show no growth at all. Half the large object is well
	// above any streaming backend's working set and well below one copy
	// of the body.
	if largeAlloc > streamLargeObject/2 {
		t.Errorf("Put allocated %s for a %s object (%.2fx the object); a "+
			"streaming backend stays far under %s.",
			mib(int64(largeAlloc)), mib(streamLargeObject),
			float64(largeAlloc)/float64(streamLargeObject), mib(streamLargeObject/2))
	}

	// A Put that allocates nothing and writes garbage is not a pass, so
	// the bytes are checked here rather than in a sibling case that could
	// be satisfied on its own.
	info, err := s.Head(ctx, largeKey)
	if err != nil {
		t.Fatalf("Head after a %s put: %v", mib(streamLargeObject), err)
	}
	if info.Size != streamLargeObject {
		t.Errorf("Head reports %d bytes, want %d", info.Size, int64(streamLargeObject))
	}
	rc, err := s.Get(ctx, largeKey)
	if err != nil {
		t.Fatalf("Get(%q): %v", largeKey, err)
	}
	defer func() { _ = rc.Close() }()
	if got, want := digestOf(rc), digestOf(newPattern(streamLargeObject)); got != want {
		t.Errorf("round-tripped digest = %s, want %s", got, want)
	}
}

// allocatedDuring reports the bytes the heap handed out while f ran.
// TotalAlloc is cumulative and never decreases, so a delta across it
// counts every allocation f made whether or not the collector reclaimed
// it — which is the question here: a buffering Put's 64 MiB is invisible
// to a live-heap reading taken once the call has returned.
func allocatedDuring(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// pattern produces deterministic bytes into the caller's buffer and holds
// nothing itself, so every byte the measurement attributes to the heap
// was allocated by the backend under test.
type pattern struct{ off, remaining int64 }

func newPattern(n int64) *pattern { return &pattern{remaining: n} }

func (p *pattern) Read(b []byte) (int, error) {
	if p.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(b))
	if n > p.remaining {
		n = p.remaining
	}
	for i := int64(0); i < n; i++ {
		b[i] = byte((p.off + i) % 251)
	}
	p.off += n
	p.remaining -= n
	return int(n), nil
}

func digestOf(r io.Reader) string {
	h := sha256.New()
	buf := make([]byte, 1<<20)
	if _, err := io.CopyBuffer(h, r, buf); err != nil {
		return "read error: " + err.Error()
	}
	return hex.EncodeToString(h.Sum(nil))
}

func mib(n int64) string { return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20)) }

func testOpenRandomAccess(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "obj", bytes.NewReader([]byte("ABCDEFGHIJ")))
	src, err := s.Open(ctx, "obj")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	if src.Size() != 10 {
		t.Errorf("size=%d, want 10", src.Size())
	}
	buf := make([]byte, 3)
	n, err := src.ReadAt(buf, 5)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n != 3 || string(buf) != "FGH" {
		t.Errorf("got %q at offset 5", buf[:n])
	}
}
