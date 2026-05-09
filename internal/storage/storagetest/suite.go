// SPDX-License-Identifier: AGPL-3.0-or-later

// Package storagetest provides a contract test suite that any
// storage.Storage implementation must pass. Backends call RunSuite
// from their own test packages with a factory that returns a fresh
// backend rooted at a clean state.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// MakeBackend returns a fresh, empty Storage. The cleanup func is
// called by the suite when each subtest finishes.
type MakeBackend func(t *testing.T) (storage.Storage, func())

// RunSuite runs every contract test against backend factories. Backend
// authors call:
//
//	storagetest.RunSuite(t, func(t *testing.T) (storage.Storage, func()) {
//	    fs, _ := local.New(t.TempDir())
//	    return fs, func() {}
//	})
func RunSuite(t *testing.T, make MakeBackend) {
	t.Helper()
	t.Run("PutThenGet", func(t *testing.T) { testPutThenGet(t, make) })
	t.Run("HeadReturnsSize", func(t *testing.T) { testHeadReturnsSize(t, make) })
	t.Run("GetMissingNotFound", func(t *testing.T) { testGetMissingNotFound(t, make) })
	t.Run("DeleteRemovesObject", func(t *testing.T) { testDeleteRemovesObject(t, make) })
	t.Run("DeleteMissingIsNoError", func(t *testing.T) { testDeleteMissingIsNoError(t, make) })
	t.Run("CopyDuplicates", func(t *testing.T) { testCopyDuplicates(t, make) })
	t.Run("ListYieldsAllKeys", func(t *testing.T) { testListYieldsAllKeys(t, make) })
	t.Run("ListPrefixFilters", func(t *testing.T) { testListPrefixFilters(t, make) })
	t.Run("ListEmptyOnMissingPrefix", func(t *testing.T) { testListEmptyOnMissingPrefix(t, make) })
	t.Run("CapabilitiesIsStable", func(t *testing.T) { testCapabilitiesIsStable(t, make) })
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

func testCopyDuplicates(t *testing.T, mk MakeBackend) {
	s, cleanup := mk(t)
	defer cleanup()
	ctx := context.Background()
	_, _ = s.Put(ctx, "src", strings.NewReader("data"))
	if _, err := s.Copy(ctx, "src", "dst"); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "dst")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != "data" {
		t.Fatalf("got %q, want %q", got, "data")
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
	it, _ := s.List(ctx, "a")
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
