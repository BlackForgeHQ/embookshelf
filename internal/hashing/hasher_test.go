package hashing_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/hashing"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// helper writes content to a temp file and returns the filename (key).
func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
	return name
}

func TestHashFile_knownInput(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	// "hello world\n" → known sha256
	key := writeFile(t, dir, "hello.txt", []byte("hello world\n"))
	want, _ := hex.DecodeString("a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447")

	digest, n, err := hashing.HashFile(context.Background(), fs, key)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if !bytes.Equal(digest, want) {
		t.Fatalf("digest=%x want=%x", digest, want)
	}
	if n != int64(len("hello world\n")) {
		t.Fatalf("n=%d want %d", n, len("hello world\n"))
	}
}

func TestHashFile_emptyFile(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	key := writeFile(t, dir, "empty.txt", []byte{})
	want, _ := hex.DecodeString("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

	digest, n, err := hashing.HashFile(context.Background(), fs, key)
	if err != nil {
		t.Fatalf("HashFile empty: %v", err)
	}
	if !bytes.Equal(digest, want) {
		t.Fatalf("digest=%x want=%x", digest, want)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

func TestHashFile_1MBRandom(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	data := make([]byte, 1<<20)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	key := writeFile(t, dir, "random.bin", data)

	_, n, err := hashing.HashFile(context.Background(), fs, key)
	if err != nil {
		t.Fatalf("HashFile 1MB: %v", err)
	}
	if n != 1<<20 {
		t.Fatalf("size=%d want %d", n, 1<<20)
	}
}

func TestHashFile_cancelledContext(t *testing.T) {
	dir := t.TempDir()
	fs, err := local.New(dir)
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}

	key := writeFile(t, dir, "some.txt", []byte("some content"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	_, _, err = hashing.HashFile(ctx, fs, key)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	// The error should wrap ctx.Err() (context.Canceled).
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v does not wrap context.Canceled", err)
	}
}
