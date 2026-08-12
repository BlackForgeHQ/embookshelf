// SPDX-License-Identifier: AGPL-3.0-or-later

package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

// TestDerivedKeyTable — one derivation for every derived artifact: the
// book's own folder (books.folder_path when set, {Author}/{Title}
// otherwise), named after the book, with the kind picking the extension
// (#299). One table, because three copies of the derivation is how the
// markdown and narration keys were free to drift.
func TestDerivedKeyTable(t *testing.T) {
	h := &service.LibraryHandle{}
	folder := "Kōbō Abe/Woman in the Dunes (2)"

	kinds := map[service.DerivedKind]string{
		service.DerivedMarkdown:  ".md",
		service.DerivedEPUB:      ".epub",
		service.DerivedNarration: ".mp3",
	}
	for kind, ext := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			placed := model.Book{
				Title: "Woman in the Dunes", Author: "Kōbō Abe", FolderPath: &folder,
			}
			if got, want := h.DerivedKey(placed, kind), folder+"/Woman in the Dunes"+ext; got != want {
				t.Errorf("with folder_path: key = %q, want %q", got, want)
			}

			bare := model.Book{Title: "Woman in the Dunes", Author: "Kōbō Abe"}
			if got, want := h.DerivedKey(bare, kind), "Kōbō Abe/Woman in the Dunes/Woman in the Dunes"+ext; got != want {
				t.Errorf("without folder_path: key = %q, want %q", got, want)
			}
		})
	}
}

// TestBookOpsResolveLibraryFailure — the resolve-library-fails arm,
// previously buried in the queue registry's inline closures and
// untestable there (#299): every operation surfaces the failure through
// the module's own interface instead of a nil dereference downstream.
func TestBookOpsResolveLibraryFailure(t *testing.T) {
	ops := service.NewBookOps(unresolvableStore{err: errors.New("backend down")}, nil)
	ctx := context.Background()
	book := model.Book{ID: "b1", LibraryID: "l1", Title: "T", Author: "A"}

	if _, _, _, err := ops.Open(ctx, book); err == nil || !strings.Contains(err.Error(), "resolve library") {
		t.Errorf("Open: err = %v, want a resolve-library failure", err)
	}
	if _, err := ops.OpenMarkdown(ctx, book, "A/T/T.md"); err == nil || !strings.Contains(err.Error(), "resolve library") {
		t.Errorf("OpenMarkdown: err = %v, want a resolve-library failure", err)
	}
	// PrimaryHash is the warn-and-degrade seam (#297): no hash, no error.
	if got := ops.PrimaryHash(ctx, book); got != nil {
		t.Errorf("PrimaryHash = %x, want nil degrade", got)
	}
}
