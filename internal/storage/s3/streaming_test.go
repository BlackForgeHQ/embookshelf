// SPDX-License-Identifier: AGPL-3.0-or-later

// Streaming-put measurements for the S3 arm.
//
// The reason the buffering put survived review is that no fake could
// disprove it: the in-memory fakes read the body into a map, so a test
// that "passes on 500 MB" says nothing about whether the adapter needed
// 500 MB of heap to get there. The assertion below is therefore not
// about size but about allocation — it puts a large object from a
// reader that materialises nothing, and measures the heap the adapter
// asked for while doing it.
//
// (The suite-level expression of the same expectation — a conformance
// case that fails any adapter which buffers — is #270's; this file keeps
// the assertion inside the package that had the bug.)
//
// Gated on TEST_S3_ENDPOINT like the rest of the S3 arm.

package s3_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/storage"
	s3backend "github.com/blackforge/embookshelf/internal/storage/s3"
)

// The two sizes the measurement compares. Both are large enough for a
// buffering adapter's allocation to stand out against SDK and transport
// noise, small enough that a local MinIO swallows them in about a
// second, and far enough apart that "allocation tracks size" and
// "allocation is bounded" cannot both be true.
const (
	putSmallObject = 16 << 20 // 16 MiB
	putLargeObject = 64 << 20 // 64 MiB
)

// putAllocBudget is what a streaming adapter is allowed to ask for,
// derived from the adapter's own bound rather than picked to fit: the
// head read grows by doubling to one part, so the transient cost of
// reaching a full part is about 2x putPartSize, and the rest is
// per-request SDK overhead. Three parts' worth leaves room for both
// without coming anywhere near a 64 MiB body.
const putAllocBudget = 3 * (8 << 20) // 24 MiB

// TestPutDoesNotAllocateProportionallyToObjectSize is the measurement.
// It fails against an adapter that reads the body into memory before
// putting it, and cannot be satisfied by a faster buffer.
//
// Two assertions, because either alone is weak. The absolute budget
// catches a buffering adapter but has to be calibrated against a
// particular part size; the growth check needs no calibration at all —
// it quadruples the object and asks whether the heap followed, which is
// exactly the claim being made.
func TestPutDoesNotAllocateProportionallyToObjectSize(t *testing.T) {
	b, cleanup := newStreamBackend(t)
	defer cleanup()

	put := func(key string, size int64) (storage.PutResult, uint64) {
		t.Helper()
		var res storage.PutResult
		var err error
		allocated := allocatedDuring(func() {
			res, err = b.Put(t.Context(), key, newPattern(size),
				storage.WithContentType("audio/mp4"))
		})
		if err != nil {
			t.Fatalf("Put(%s): %v", mib(size), err)
		}
		return res, allocated
	}

	_, smallAlloc := put("narration/short.m4b", putSmallObject)
	res, largeAlloc := put("narration/full-length.m4b", putLargeObject)

	t.Logf("put of %s allocated %s; put of %s allocated %s (budget %s), etag %q",
		mib(putSmallObject), mib(int64(smallAlloc)),
		mib(putLargeObject), mib(int64(largeAlloc)),
		mib(putAllocBudget), res.ETag)

	if largeAlloc > putAllocBudget {
		t.Errorf("Put allocated %s for a %s object (%.2fx the object); a "+
			"streaming adapter must stay under %s. The adapter is holding "+
			"the whole body in memory.",
			mib(int64(largeAlloc)), mib(putLargeObject),
			float64(largeAlloc)/float64(putLargeObject), mib(putAllocBudget))
	}

	// Quadrupling the object must not carry the heap with it. The slack
	// is a quarter of the *small* object, so an adapter whose cost tracks
	// size fails by an order of magnitude while genuine per-part overhead
	// (eight requests instead of two) passes unremarked.
	const slack = putSmallObject / 4
	if largeAlloc > smallAlloc+slack {
		t.Errorf("quadrupling the object from %s to %s took allocation from "+
			"%s to %s, a rise of %s. Put's cost tracks the object rather than "+
			"being bounded by the part size.",
			mib(putSmallObject), mib(putLargeObject),
			mib(int64(smallAlloc)), mib(int64(largeAlloc)),
			mib(int64(largeAlloc-smallAlloc)))
	}

	// A put that allocates little but writes garbage is not a fix, so the
	// bytes are checked in the same test rather than a sibling that could
	// be satisfied independently.
	const key = "narration/full-length.m4b"
	info, err := b.Head(t.Context(), key)
	if err != nil {
		t.Fatalf("Head after a large put: %v", err)
	}
	if info.Size != putLargeObject {
		t.Errorf("Head reports %d bytes, want %d", info.Size, int64(putLargeObject))
	}
	if info.ContentType != "audio/mp4" {
		t.Errorf("ContentType = %q, want %q — a multipart put must carry it "+
			"the same way a single put did", info.ContentType, "audio/mp4")
	}
	if got, want := readDigest(t, b, key), patternDigest(putLargeObject); got != want {
		t.Errorf("round-tripped digest = %s, want %s", got, want)
	}
	if res.ETag == "" {
		t.Error("PutResult.ETag is empty; callers read it as the handle on " +
			"the version just written")
	}
}

// TestPutPartBoundaries walks the sizes where the single/multipart
// choice and the part loop change their minds. Each has to round-trip
// byte-for-byte at the size the caller asked for; an off-by-one in the
// head read or the refill would show up here and nowhere else.
func TestPutPartBoundaries(t *testing.T) {
	b, cleanup := newStreamBackend(t)
	defer cleanup()

	const part = 8 << 20 // must track putPartSize in methods.go
	for _, size := range []int64{
		0, 1, part - 1, part, part + 1, 2 * part, 2*part + 7,
	} {
		t.Run(fmt.Sprintf("%d", size), func(t *testing.T) {
			key := fmt.Sprintf("boundary/%d.bin", size)
			res, err := b.Put(t.Context(), key, newPattern(size))
			if err != nil {
				t.Fatalf("Put(%d bytes): %v", size, err)
			}
			if res.ETag == "" {
				t.Error("PutResult.ETag is empty")
			}
			info, err := b.Head(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size != size {
				t.Errorf("Head size = %d, want %d", info.Size, size)
			}
			if got, want := readDigest(t, b, key), patternDigest(size); got != want {
				t.Errorf("digest = %s, want %s", got, want)
			}
			// Head has to keep agreeing with Put about the ETag, because
			// that round trip is what makes WithIfMatch usable — and the
			// multipart form is where the two could drift apart.
			if info.ETag != res.ETag {
				t.Errorf("Head ETag %q != Put ETag %q", info.ETag, res.ETag)
			}
		})
	}
}

// TestPutConditionalOnLargeObject pins that the conditional contract
// survives whatever path a large body takes. The conformance suite only
// ever exercises it on a few bytes, so a multipart branch could drop
// If-None-Match without any existing test noticing.
func TestPutConditionalOnLargeObject(t *testing.T) {
	b, cleanup := newStreamBackend(t)
	defer cleanup()

	const key = "narration/conditional.m4b"
	// Just over one part, so the object is guaranteed to take whichever
	// path a large body takes rather than the small-object shortcut.
	const size = 12 << 20

	if _, err := b.Put(t.Context(), key, newPattern(size),
		storage.WithIfNoneMatch("*")); err != nil {
		t.Fatalf("Put(WithIfNoneMatch(*)) on an absent key = %v, want success", err)
	}

	_, err := b.Put(t.Context(), key, newPattern(size), storage.WithIfNoneMatch("*"))
	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("Put(WithIfNoneMatch(*)) over an existing large object = %v, "+
			"want ErrPreconditionFailed", err)
	}
	if got, want := readDigest(t, b, key), patternDigest(size); got != want {
		t.Errorf("a refused If-None-Match put changed the object: digest %s, want %s", got, want)
	}

	info, err := b.Head(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(t.Context(), key, newPattern(size),
		storage.WithIfMatch(info.ETag)); err != nil {
		t.Fatalf("Put(WithIfMatch(%q)) with the current ETag = %v, want success",
			info.ETag, err)
	}

	_, err = b.Put(t.Context(), key, newPattern(size),
		storage.WithIfMatch("00000000000000000000000000000000"))
	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("Put(WithIfMatch(stale)) on a large object = %v, want "+
			"ErrPreconditionFailed", err)
	}
}

// newStreamBackend builds a backend on its own scratch prefix, the same
// way the conformance arm does.
func newStreamBackend(t *testing.T) (*s3backend.Backend, func()) {
	t.Helper()
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set TEST_S3_ENDPOINT to run the S3 streaming measurements " +
			"(`make test-s3` starts one)")
	}
	bucket := os.Getenv("TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "embookshelf-test"
	}
	mk := func(prefix string) *s3backend.Backend {
		b, err := s3backend.New(t.Context(), s3backend.Config{
			Endpoint:        endpoint,
			Region:          "us-east-1",
			Bucket:          bucket,
			Prefix:          prefix,
			AccessKeyID:     os.Getenv("TEST_S3_AK"),
			SecretAccessKey: os.Getenv("TEST_S3_SK"),
			ForcePathStyle:  true,
			SkipValidation:  true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ensureBucket(t, mk("").Client(), bucket)

	prefix := fmt.Sprintf("stream-%d-%d/", time.Now().UnixNano(), os.Getpid())
	b := mk(prefix)
	return b, func() { purgePrefix(t, b.Client(), bucket, prefix) }
}

// allocatedDuring reports the bytes the heap handed out while f ran.
// TotalAlloc is cumulative and never decreases, so a delta across it
// counts every allocation f made whether or not the collector reclaimed
// it — which is the question here: a buffering put's 64 MiB is invisible
// to a live-heap reading taken after the request completed.
func allocatedDuring(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// pattern is a reader that produces deterministic bytes into the
// caller's buffer and holds nothing itself, so every byte the
// measurement attributes to the heap was allocated by the adapter.
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

func patternDigest(n int64) string { return digestOf(newPattern(n)) }

func readDigest(t *testing.T, b *s3backend.Backend, key string) string {
	t.Helper()
	rc, err := b.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	return digestOf(rc)
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
