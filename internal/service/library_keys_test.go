// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"path/filepath"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

// The keys seam is pure, and these are its rules stated as a table
// (#346). The handle-level tests exercise the same arithmetic through
// Walk/PlaceAt/StorageKey; this pins the value on its own, which is
// what the carve bought.
func TestLibraryKeysRoot(t *testing.T) {
	root := "/lib/root"
	cases := []struct {
		name     string
		keys     libraryKeys
		wantRoot string
		wantOK   bool
	}{
		{
			name:     "object store owns its own prefix — empty root by design",
			keys:     libraryKeys{lib: model.Library{ID: "l"}, objectStore: true},
			wantRoot: "", wantOK: true,
		},
		{
			name:     "local library roots at its own directory",
			keys:     libraryKeys{lib: model.Library{ID: "l", Root: &root}},
			wantRoot: root, wantOK: true,
		},
		{
			name:     "unconfigured local library has no root — not the same empty",
			keys:     libraryKeys{lib: model.Library{ID: "l"}},
			wantRoot: "", wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := c.keys.root()
			if got != c.wantRoot || ok != c.wantOK {
				t.Fatalf("root() = (%q, %v), want (%q, %v)", got, ok, c.wantRoot, c.wantOK)
			}
		})
	}
}

func TestLibraryKeysStorageKey(t *testing.T) {
	root := "/lib/root"
	local := libraryKeys{lib: model.Library{ID: "l", Root: &root}}
	object := libraryKeys{lib: model.Library{ID: "l"}, objectStore: true}
	unrooted := libraryKeys{lib: model.Library{ID: "l"}}

	if got, want := local.storageKey("A/B/c.epub"), filepath.Join(root, "A/B/c.epub"); got != want {
		t.Errorf("local relative = %q, want rooted %q", got, want)
	}
	// A legacy absolute location is already the key a "/"-rooted LocalFS
	// wants; joining it would ask for /lib/root/lib/root/… (#201).
	if got := local.storageKey("/abs/elsewhere.epub"); got != "/abs/elsewhere.epub" {
		t.Errorf("local absolute = %q, want it untouched", got)
	}
	if got := object.storageKey("A/B/c.epub"); got != "A/B/c.epub" {
		t.Errorf("object store = %q, want the location it already answers to", got)
	}
	if got := unrooted.storageKey("A/B/c.epub"); got != "A/B/c.epub" {
		t.Errorf("unrooted = %q, want the location back — callers gate on root() first", got)
	}
	if got := object.localPath("A/B/c.epub"); got != "" {
		t.Errorf("object store localPath = %q, want empty — no filesystem to resolve against", got)
	}
}
