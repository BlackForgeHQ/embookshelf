// SPDX-License-Identifier: AGPL-3.0-or-later

package s3

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
)

// stubLister replays a fixed key set. The full contract test runs
// against a real service behind the s3integration tag; these cover the
// copy loop's bookkeeping, which is the part a caller depends on and
// which no amount of minio makes easier to provoke.
type stubLister struct {
	keys []string
	err  error
}

func (s stubLister) List(_ context.Context, prefix string) (storage.Iterator, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []string
	for _, k := range s.keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return &sliceIter{keys: out}, nil
}

type sliceIter struct{ keys []string }

func (it *sliceIter) Next(context.Context) (storage.ObjectInfo, error) {
	if len(it.keys) == 0 {
		return storage.ObjectInfo{}, io.EOF
	}
	k := it.keys[0]
	it.keys = it.keys[1:]
	return storage.ObjectInfo{Key: k}, nil
}
func (it *sliceIter) Close() error { return nil }

func TestMovePrefix_ReportsWrittenAndReclaim(t *testing.T) {
	lister := stubLister{keys: []string{
		"Tolkien/Hobbit/hobbit.epub",
		"Tolkien/Hobbit/metadata.embookshelf.json",
		"Tolkien/Hobbit Revisited/other.epub", // must not be swept in
	}}
	var copies [][2]string
	res, err := movePrefix(context.Background(), lister, "Tolkien/Hobbit", "Tolkien/The Hobbit/",
		func(_ context.Context, src, dst string) error {
			copies = append(copies, [2]string{src, dst})
			return nil
		})
	if err != nil {
		t.Fatalf("movePrefix: %v", err)
	}
	wantWritten := []string{
		"Tolkien/The Hobbit/hobbit.epub",
		"Tolkien/The Hobbit/metadata.embookshelf.json",
	}
	if strings.Join(res.Written, "|") != strings.Join(wantWritten, "|") {
		t.Errorf("Written=%v want %v", res.Written, wantWritten)
	}
	wantReclaim := []string{
		"Tolkien/Hobbit/hobbit.epub",
		"Tolkien/Hobbit/metadata.embookshelf.json",
	}
	if strings.Join(res.Reclaim, "|") != strings.Join(wantReclaim, "|") {
		t.Errorf("Reclaim=%v want %v — the sources stay live for in-flight "+
			"presigned URLs and the caller defers the delete", res.Reclaim, wantReclaim)
	}
	if len(copies) != 2 {
		t.Errorf("copies=%v want 2 (the sibling prefix must not be included)", copies)
	}
}

// A copy failure halfway through must still hand back what it wrote:
// the caller is the only party that can reclaim those objects, and
// dropping them on the floor leaks a partial rename.
func TestMovePrefix_PartialWriteTravelsWithTheError(t *testing.T) {
	lister := stubLister{keys: []string{"old/a", "old/b", "old/c"}}
	boom := errors.New("throttled")
	res, err := movePrefix(context.Background(), lister, "old", "new",
		func(_ context.Context, src, _ string) error {
			if src == "old/b" {
				return boom
			}
			return nil
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v want %v", err, boom)
	}
	if len(res.Written) != 1 || res.Written[0] != "new/a" {
		t.Errorf("Written=%v want [new/a]", res.Written)
	}
	if len(res.Reclaim) != 0 {
		t.Errorf("Reclaim=%v want empty — a failed move reclaims nothing on "+
			"the source side", res.Reclaim)
	}
}

func TestMovePrefix_EmptySourceIsNotFound(t *testing.T) {
	res, err := movePrefix(context.Background(), stubLister{keys: []string{"other/a"}},
		"old", "new", func(context.Context, string, string) error {
			t.Fatal("copy must not be attempted for a missing prefix")
			return nil
		})
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("err=%v want ErrNotFound", err)
	}
	if len(res.Written) != 0 || len(res.Reclaim) != 0 {
		t.Errorf("res=%+v want empty", res)
	}
}

func TestMovePrefix_RefusesTheWholeBackend(t *testing.T) {
	neverCopy := func(context.Context, string, string) error {
		t.Fatal("copy must not be attempted for an empty prefix")
		return nil
	}
	for _, p := range []string{"", "/"} {
		if _, err := movePrefix(context.Background(), stubLister{}, p, "new",
			neverCopy); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("movePrefix(old=%q) = %v, want ErrInvalidKey", p, err)
		}
		if _, err := movePrefix(context.Background(), stubLister{}, "old", p,
			neverCopy); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("movePrefix(new=%q) = %v, want ErrInvalidKey", p, err)
		}
	}
}

func TestFolderPrefix(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"/":                "",
		"a":                "a/",
		"a/":               "a/",
		"/a/b":             "a/b/",
		"Author/Title///":  "Author/Title/",
		"Author/Title (2)": "Author/Title (2)/",
	}
	for in, want := range cases {
		if got := folderPrefix(in); got != want {
			t.Errorf("folderPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}
