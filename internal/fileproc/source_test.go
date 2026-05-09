// SPDX-License-Identifier: AGPL-3.0-or-later

package fileproc

import (
	"bytes"
	"os"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// memSource wraps a byte slice as a storage.Source for tests.
type memSource struct {
	*bytes.Reader
	size int64
}

func (m *memSource) Size() int64  { return m.size }
func (m *memSource) Close() error { return nil }

func memSourceFromBytes(b []byte) storage.Source {
	return &memSource{Reader: bytes.NewReader(b), size: int64(len(b))}
}

func memSourceFromFile(t *testing.T, path string) storage.Source {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return memSourceFromBytes(b)
}

// TestMemSource_ReadAt verifies that memSource satisfies the storage.Source
// contract: ReadAt at arbitrary offsets returns the right bytes.
func TestMemSource_ReadAt(t *testing.T) {
	data := []byte("hello, world")
	src := memSourceFromBytes(data)
	defer func() { _ = src.Close() }()

	if src.Size() != int64(len(data)) {
		t.Fatalf("Size=%d, want %d", src.Size(), len(data))
	}

	buf := make([]byte, 5)
	n, err := src.ReadAt(buf, 7)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != 5 || string(buf) != "world" {
		t.Errorf("got %q, want %q", buf[:n], "world")
	}
}

// TestMemSource_FromFile verifies that memSourceFromFile reads a real file
// into a memSource with the correct size and content.
func TestMemSource_FromFile(t *testing.T) {
	// Write a temp file and read it back via memSourceFromFile.
	f, err := os.CreateTemp(t.TempDir(), "src-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("test payload 42")
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	name := f.Name()
	_ = f.Close()

	src := memSourceFromFile(t, name)
	defer func() { _ = src.Close() }()

	if src.Size() != int64(len(payload)) {
		t.Fatalf("Size=%d, want %d", src.Size(), len(payload))
	}
	buf := make([]byte, src.Size())
	n, _ := src.ReadAt(buf, 0)
	if string(buf[:n]) != string(payload) {
		t.Errorf("content mismatch: got %q, want %q", buf[:n], payload)
	}
}
