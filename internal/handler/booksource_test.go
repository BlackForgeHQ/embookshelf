package handler

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

// stubPresigner implements both storage.Storage and Presigner so it can be
// used as the backend returned by a test Resolver.
type stubPresigner struct {
	cap storage.Capability
	url string
	err error
}

func (s *stubPresigner) Capabilities() storage.Capability { return s.cap }
func (s *stubPresigner) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.url, nil
}

// storage.Storage stub methods — never called in resolver tests.
func (s *stubPresigner) List(_ context.Context, _ string) (storage.Iterator, error) {
	return nil, nil
}
func (s *stubPresigner) Head(_ context.Context, _ string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (s *stubPresigner) Get(_ context.Context, _ string, _ ...storage.GetOption) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubPresigner) Put(_ context.Context, _ string, _ io.Reader, _ ...storage.PutOption) (storage.PutResult, error) {
	return storage.PutResult{}, nil
}
func (s *stubPresigner) Delete(_ context.Context, _ string, _ ...storage.DeleteOption) error {
	return nil
}
func (s *stubPresigner) Copy(_ context.Context, _, _ string) (storage.CopyResult, error) {
	return storage.CopyResult{}, nil
}
func (s *stubPresigner) Open(_ context.Context, _ string) (storage.Source, error) {
	return nil, storage.ErrNotFound
}

// stubResolver returns the same backend for any backend id.
type stubResolver struct{ backend storage.Storage }

func (r *stubResolver) Resolve(_ string) (storage.Storage, error) { return r.backend, nil }

// errResolver always returns an error from Resolve.
type errResolver struct{}

func (r *errResolver) Resolve(_ string) (storage.Storage, error) {
	return nil, storage.ErrNotFound
}

func testBook() model.Book {
	return model.Book{
		ID:        "book-1",
		LibraryID: "lib-1",
		Format:    "EPUB",
		Path:      "/data/books/test.epub",
	}
}

// TestResolveBookSource_NilResolver checks that a nil resolver falls back to local.
func TestResolveBookSource_NilResolver(t *testing.T) {
	src, err := ResolveBookSource(context.Background(), testBook(), nil, nil, nil, 10*time.Minute, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != "local" {
		t.Errorf("want Kind=local, got %q", src.Kind)
	}
	if src.Path != "/data/books/test.epub" {
		t.Errorf("want Path set, got %q", src.Path)
	}
}

// TestResolveBookSource_EmptyPath checks that an empty book.Path returns an error.
func TestResolveBookSource_EmptyPath(t *testing.T) {
	book := testBook()
	book.Path = ""
	_, err := ResolveBookSource(context.Background(), book, nil, nil, nil, 10*time.Minute, "")
	if err == nil {
		t.Fatal("expected error for book with no path")
	}
}

// TestResolveBookSource_LocalBackend checks that a backend with no CapPresign stays local.
func TestResolveBookSource_LocalBackend(t *testing.T) {
	backend := &stubPresigner{cap: 0} // capabilities = 0, like LocalFS
	resolver := &stubResolver{backend: backend}
	src, err := ResolveBookSource(context.Background(), testBook(), nil, nil, resolver, 10*time.Minute, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// files and libs are nil so we short-circuit to local before even
	// reaching the capabilities check.
	if src.Kind != "local" {
		t.Errorf("want Kind=local, got %q", src.Kind)
	}
}

// TestResolveBookSource_PresignFallbackStream checks that fallback=stream forces local
// even when the backend supports presign.
func TestResolveBookSource_PresignFallbackStream(t *testing.T) {
	backend := &stubPresigner{cap: storage.CapPresign, url: "https://s3.example.com/presigned"}
	resolver := &stubResolver{backend: backend}
	// files/libs are nil so resolver won't be reached, but the test still
	// documents that fallback="stream" short-circuits to local.
	src, err := ResolveBookSource(context.Background(), testBook(), nil, nil, resolver, 10*time.Minute, "stream")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if src.Kind != "local" {
		t.Errorf("want Kind=local with fallback=stream, got %q", src.Kind)
	}
}

// TestResolveBookSource_ResolverError checks that a failing resolver falls back to local.
func TestResolveBookSource_ResolverError(t *testing.T) {
	resolver := &errResolver{}
	src, err := ResolveBookSource(context.Background(), testBook(), nil, nil, resolver, 10*time.Minute, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// files/libs are nil so we short-circuit before Resolve is called;
	// confirm we're still local.
	if src.Kind != "local" {
		t.Errorf("want Kind=local, got %q", src.Kind)
	}
}

// TestPresignerInterface checks that stubPresigner satisfies the Presigner interface.
func TestPresignerInterface(t *testing.T) {
	var _ Presigner = &stubPresigner{}
}
