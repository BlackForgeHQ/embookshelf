// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

func TestNew_RejectsRelativeRoot(t *testing.T) {
	_, err := New("relative/path")
	if err == nil {
		t.Fatal("expected error for relative root, got nil")
	}
}

func TestNew_AcceptsAbsoluteRoot(t *testing.T) {
	root := t.TempDir()
	if !filepath.IsAbs(root) {
		t.Fatalf("t.TempDir returned non-absolute path: %q", root)
	}
	fs, err := New(root)
	if err != nil {
		t.Fatalf("New(%q): %v", root, err)
	}
	if fs == nil {
		t.Fatal("New returned nil")
	}
}

func TestPut_WritesBytesAtomically(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := fs.Put(ctx, "a/b/file.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "a", "b", "file.txt"))
	if err != nil {
		t.Fatalf("read after Put: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("file contents = %q, want %q", got, "hello")
	}
	if res.ETag != "" {
		t.Logf("ETag returned = %q (informational; LocalFS may leave empty)", res.ETag)
	}
}

func TestPut_NoTempFilesLeftOnSuccess(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	if _, err := fs.Put(context.Background(), "x.txt", bytes.NewReader([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestGet_ReturnsBytes(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	if _, err := fs.Put(ctx, "f", strings.NewReader("contents")); err != nil {
		t.Fatal(err)
	}
	rc, err := fs.Get(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "contents" {
		t.Fatalf("got %q, want %q", got, "contents")
	}
}

func TestGet_MissingReturnsErrNotFound(t *testing.T) {
	fs, _ := New(t.TempDir())
	_, err := fs.Get(context.Background(), "nope")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want storage.ErrNotFound", err)
	}
}

func TestHead_ReturnsSizeAndMtime(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("hi"))
	info, err := fs.Head(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != 2 {
		t.Errorf("Size = %d, want 2", info.Size)
	}
	if info.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}
	if info.Key != "f" {
		t.Errorf("Key = %q, want %q", info.Key, "f")
	}
}

func TestDelete_RemovesFile(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("x"))
	if err := fs.Delete(ctx, "f"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Head(ctx, "f"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("after Delete, Head returned %v, want ErrNotFound", err)
	}
}

func TestDelete_MissingIsNoError(t *testing.T) {
	fs, _ := New(t.TempDir())
	if err := fs.Delete(context.Background(), "nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestList_WalksRecursivelyAndYieldsRelativeKeys(t *testing.T) {
	root := t.TempDir()
	fsys, _ := New(root)
	ctx := context.Background()
	for _, k := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		if _, err := fsys.Put(ctx, k, strings.NewReader(k)); err != nil {
			t.Fatal(err)
		}
	}
	it, err := fsys.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	got := map[string]bool{}
	for {
		o, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got[o.Key] = true
	}
	for _, want := range []string{"a.txt", "sub/b.txt", "sub/deep/c.txt"} {
		if !got[want] {
			t.Errorf("missing key %q", want)
		}
	}
}

func TestList_PrefixFilter(t *testing.T) {
	root := t.TempDir()
	fsys, _ := New(root)
	ctx := context.Background()
	for _, k := range []string{"a/x", "a/y", "b/z"} {
		_, _ = fsys.Put(ctx, k, strings.NewReader(""))
	}
	it, err := fsys.List(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
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
		t.Errorf("got %d entries, want 2", count)
	}
}

func TestList_MissingPrefixReturnsEmpty(t *testing.T) {
	fsys, _ := New(t.TempDir())
	it, err := fsys.List(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	defer it.Close()
	_, err = it.Next(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
}

func TestOpen_RandomAccess(t *testing.T) {
	root := t.TempDir()
	fs, _ := New(root)
	ctx := context.Background()
	_, _ = fs.Put(ctx, "f", strings.NewReader("0123456789"))
	src, err := fs.Open(ctx, "f")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	if src.Size() != 10 {
		t.Errorf("Size=%d, want 10", src.Size())
	}
	buf := make([]byte, 4)
	n, err := src.ReadAt(buf, 3)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if n != 4 || string(buf) != "3456" {
		t.Errorf("got %q, want %q", buf[:n], "3456")
	}
}

func TestOpen_MissingReturnsErrNotFound(t *testing.T) {
	fs, _ := New(t.TempDir())
	_, err := fs.Open(context.Background(), "nope")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
