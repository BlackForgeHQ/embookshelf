package local

import (
	"path/filepath"
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
