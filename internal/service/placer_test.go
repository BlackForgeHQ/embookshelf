// SPDX-License-Identifier: AGPL-3.0-or-later

package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLocalPlacer_FolderLayout(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "hobbit.epub", "epub bytes")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "J.R.R. Tolkien",
		Title:  "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantLocation := filepath.Join("J.R.R. Tolkien", "The Hobbit", "hobbit.epub")
	if res.Location != wantLocation {
		t.Errorf("Location=%q want %q", res.Location, wantLocation)
	}
	wantFolder := filepath.Join("J.R.R. Tolkien", "The Hobbit")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}

	on := filepath.Join(root, wantLocation)
	if _, err := os.Stat(on); err != nil {
		t.Fatalf("stat moved file: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source still exists after place: err=%v", err)
	}
}

func TestLocalPlacer_FallbackSentinels(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "mystery.epub", "x")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "",
		Title:  "",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantFolder := filepath.Join("Unknown Author", "Untitled")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}
}

func TestLocalPlacer_Sanitization(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	src := writeTempFile(t, staging, "weird.epub", "x")

	p := LocalPlacer{Root: root}
	res, err := p.Place(context.Background(), PlaceSource{
		Path:   src,
		Format: "EPUB",
		Author: "Author/With/Slashes",
		Title:  "Title:With*Stars",
	})
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	wantFolder := filepath.Join("Author_With_Slashes", "Title_With_Stars")
	if res.FolderPath != wantFolder {
		t.Errorf("FolderPath=%q want %q", res.FolderPath, wantFolder)
	}
}

func TestLocalPlacer_FolderCollisionGetsSuffix(t *testing.T) {
	root := t.TempDir()
	stagingA := t.TempDir()
	stagingB := t.TempDir()
	srcA := writeTempFile(t, stagingA, "edition1.epub", "a")
	srcB := writeTempFile(t, stagingB, "edition2.epub", "b")

	p := LocalPlacer{Root: root}

	resA, err := p.Place(context.Background(), PlaceSource{
		Path: srcA, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place A: %v", err)
	}
	if resA.FolderPath != filepath.Join("Tolkien", "The Hobbit") {
		t.Fatalf("A FolderPath=%q", resA.FolderPath)
	}

	resB, err := p.Place(context.Background(), PlaceSource{
		Path: srcB, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place B: %v", err)
	}
	wantB := filepath.Join("Tolkien", "The Hobbit (2)")
	if resB.FolderPath != wantB {
		t.Errorf("B FolderPath=%q want %q", resB.FolderPath, wantB)
	}

	// Both files exist on disk.
	for _, want := range []string{
		filepath.Join(root, resA.Location),
		filepath.Join(root, resB.Location),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("missing file %q: %v", want, err)
		}
	}
}

func TestLocalPlacer_FileCollisionWithinSameFolder(t *testing.T) {
	root := t.TempDir()
	stagingA := t.TempDir()
	stagingB := t.TempDir()
	srcA := writeTempFile(t, stagingA, "hobbit.epub", "a")
	srcB := writeTempFile(t, stagingB, "hobbit.epub", "b")

	p := LocalPlacer{Root: root}

	if _, err := p.Place(context.Background(), PlaceSource{
		Path: srcA, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	}); err != nil {
		t.Fatalf("Place A: %v", err)
	}

	// Pre-create a placeholder so the directory exists; force B to also
	// see "hobbit.epub" present. Without uniqueDirectory's title suffix
	// we'd land in the same folder; assert that we either get a unique
	// directory OR a unique file basename.
	resB, err := p.Place(context.Background(), PlaceSource{
		Path: srcB, Format: "EPUB", Author: "Tolkien", Title: "The Hobbit",
	})
	if err != nil {
		t.Fatalf("Place B: %v", err)
	}
	if resB.Location == filepath.Join("Tolkien", "The Hobbit", "hobbit.epub") {
		t.Errorf("B overwrote A: Location=%q", resB.Location)
	}
}

func TestLocalPlacer_EmptyRoot(t *testing.T) {
	p := LocalPlacer{Root: ""}
	_, err := p.Place(context.Background(), PlaceSource{Path: "/tmp/x"})
	if err == nil {
		t.Fatal("expected error on empty root")
	}
}
