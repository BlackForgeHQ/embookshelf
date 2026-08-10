// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/storage"
)

type memSource struct {
	*bytes.Reader
}

func (m memSource) Size() int64  { return int64(m.Len()) }
func (m memSource) Close() error { return nil }

type fakeDropStore struct {
	objects map[string][]byte
	deleted []string
	openErr error
}

type sliceIter struct {
	items []storage.ObjectInfo
	i     int
}

func (s *sliceIter) Next(context.Context) (storage.ObjectInfo, error) {
	if s.i >= len(s.items) {
		return storage.ObjectInfo{}, io.EOF
	}
	s.i++
	return s.items[s.i-1], nil
}

func (s *sliceIter) Close() error { return nil }

func (f *fakeDropStore) List(_ context.Context, _ string) (storage.Iterator, error) {
	var out []storage.ObjectInfo
	for k := range f.objects {
		out = append(out, storage.ObjectInfo{Key: k})
	}
	return &sliceIter{items: out}, nil
}

func (f *fakeDropStore) Open(_ context.Context, key string) (storage.Source, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return memSource{bytes.NewReader(f.objects[key])}, nil
}

func (f *fakeDropStore) Delete(_ context.Context, key string, _ ...storage.DeleteOption) error {
	f.deleted = append(f.deleted, key)
	delete(f.objects, key)
	return nil
}

type acceptCall struct {
	name string
	body string
}

func acceptRecorder(calls *[]acceptCall, fail error) func(context.Context, string, io.Reader) (model.BookDropItem, error) {
	return func(_ context.Context, name string, src io.Reader) (model.BookDropItem, error) {
		if fail != nil {
			return model.BookDropItem{}, fail
		}
		b, _ := io.ReadAll(src)
		*calls = append(*calls, acceptCall{name: name, body: string(b)})
		return model.BookDropItem{ID: "item"}, nil
	}
}

// TestS3WatcherPullsSupportedObjectsThenDeletes — the happy path: bytes
// reach Accept intact under the object's basename, and the object dies
// only afterwards.
func TestS3WatcherPullsSupportedObjectsThenDeletes(t *testing.T) {
	store := &fakeDropStore{objects: map[string][]byte{
		"bookdrop/dune.epub":  []byte("epub-bytes"),
		"bookdrop/notes.txt~": []byte("junk"),
	}}
	var calls []acceptCall
	w := &S3Watcher{Store: store, Prefix: "bookdrop", Accept: acceptRecorder(&calls, nil)}

	w.scan(context.Background())

	if len(calls) != 1 || calls[0].name != "dune.epub" || calls[0].body != "epub-bytes" {
		t.Fatalf("accept calls = %+v", calls)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "bookdrop/dune.epub" {
		t.Fatalf("deleted = %v", store.deleted)
	}
	// The unsupported object is left alone — not ours to delete.
	if _, ok := store.objects["bookdrop/notes.txt~"]; !ok {
		t.Fatal("unsupported object was removed")
	}
}

// TestS3WatcherKeepsTheObjectWhenAcceptFails — the only destructive
// step is last; a failed intake leaves the object for the next tick.
func TestS3WatcherKeepsTheObjectWhenAcceptFails(t *testing.T) {
	store := &fakeDropStore{objects: map[string][]byte{
		"bookdrop/dune.epub": []byte("epub-bytes"),
	}}
	var calls []acceptCall
	w := &S3Watcher{Store: store, Prefix: "bookdrop", Accept: acceptRecorder(&calls, errors.New("staging disk full"))}

	w.scan(context.Background())

	if len(store.deleted) != 0 {
		t.Fatalf("deleted = %v, want nothing on a failed accept", store.deleted)
	}
	if _, ok := store.objects["bookdrop/dune.epub"]; !ok {
		t.Fatal("object vanished despite the failed accept")
	}
}

func TestDropPrefixCollides(t *testing.T) {
	cases := []struct {
		drop, lib string
		want      bool
	}{
		{"bookdrop", "libraries/main", false},
		{"bookdrop", "bookdrop", true},
		{"bookdrop", "bookdrop/sub", true},
		{"bookdrop/sub", "bookdrop", true},
		{"bookdrop", "", true},        // bucket-rooted library contains everything
		{"", "libraries", false},      // no drop prefix, no watcher
		{"books", "bookshelf", false}, // shared string prefix but different path segment
	}
	for _, tc := range cases {
		if got := DropPrefixCollides(tc.drop, tc.lib); got != tc.want {
			t.Errorf("DropPrefixCollides(%q, %q) = %v, want %v", tc.drop, tc.lib, got, tc.want)
		}
	}
}
