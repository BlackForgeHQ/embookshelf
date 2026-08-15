// SPDX-License-Identifier: AGPL-3.0-or-later

package storage_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// stubStorage is a minimal Storage implementation for resolver tests.
type stubStorage struct{ id string }

func (s *stubStorage) Capabilities() storage.Capability { return 0 }
func (s *stubStorage) List(_ context.Context, _ string) (storage.Iterator, error) {
	return nil, nil
}
func (s *stubStorage) Head(_ context.Context, _ string) (storage.ObjectInfo, error) {
	return storage.ObjectInfo{}, nil
}
func (s *stubStorage) Get(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *stubStorage) Put(_ context.Context, _ string, _ io.Reader, _ ...storage.PutOption) (storage.PutResult, error) {
	return storage.PutResult{}, nil
}
func (s *stubStorage) Delete(_ context.Context, _ string) error {
	return nil
}
func (s *stubStorage) MovePrefix(_ context.Context, _, _ string) error {
	return nil
}
func (s *stubStorage) Open(_ context.Context, _ string) (storage.Source, error) {
	return nil, storage.ErrNotFound
}

func TestConstantResolver_AlwaysReturnsBackend(t *testing.T) {
	s := &stubStorage{id: "main"}
	r := storage.ConstantResolver{S: s}

	got, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve empty: %v", err)
	}
	if got != s {
		t.Fatal("expected same storage instance")
	}

	got, err = r.Resolve("anything")
	if err != nil {
		t.Fatalf("Resolve non-empty: %v", err)
	}
	if got != s {
		t.Fatal("expected same storage instance for non-empty id")
	}
}

func TestConstantResolver_NilReturnsError(t *testing.T) {
	r := storage.ConstantResolver{}
	_, err := r.Resolve("")
	if err == nil {
		t.Fatal("expected error for nil backend")
	}
}

func TestMapResolver_RoutesByID(t *testing.T) {
	a := &stubStorage{id: "a"}
	b := &stubStorage{id: "b"}
	def := &stubStorage{id: "default"}

	r := &storage.MapResolver{
		Default: def,
		Backends: map[string]storage.Storage{
			"a": a,
			"b": b,
		},
	}

	got, err := r.Resolve("a")
	if err != nil {
		t.Fatalf("Resolve a: %v", err)
	}
	if got != a {
		t.Fatal("expected backend a")
	}

	got, err = r.Resolve("b")
	if err != nil {
		t.Fatalf("Resolve b: %v", err)
	}
	if got != b {
		t.Fatal("expected backend b")
	}
}

func TestMapResolver_EmptyIDReturnsDefault(t *testing.T) {
	def := &stubStorage{id: "default"}
	r := &storage.MapResolver{
		Default:  def,
		Backends: map[string]storage.Storage{},
	}

	got, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve empty: %v", err)
	}
	if got != def {
		t.Fatal("expected default storage")
	}
}

func TestMapResolver_EmptyIDNoDefaultReturnsError(t *testing.T) {
	r := &storage.MapResolver{
		Default:  nil,
		Backends: map[string]storage.Storage{},
	}
	_, err := r.Resolve("")
	if err == nil {
		t.Fatal("expected error when no default configured")
	}
}

func TestMapResolver_UnknownIDReturnsError(t *testing.T) {
	r := &storage.MapResolver{
		Default:  &stubStorage{},
		Backends: map[string]storage.Storage{},
	}
	_, err := r.Resolve("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown backend id")
	}
}

func TestResolverFunc_Delegates(t *testing.T) {
	s := &stubStorage{id: "fn"}
	fn := storage.ResolverFunc(func(id string) (storage.Storage, error) {
		if id == "fn" {
			return s, nil
		}
		return nil, errors.New("not found")
	})

	got, err := fn.Resolve("fn")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != s {
		t.Fatal("expected fn storage")
	}

	_, err = fn.Resolve("other")
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}
