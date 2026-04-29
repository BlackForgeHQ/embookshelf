package local

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
