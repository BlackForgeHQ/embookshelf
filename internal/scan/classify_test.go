package scan

import (
	"strings"
	"testing"
)

// supportedExt fakes the fileproc.IsSupported gate for tests: everything
// ending in one of the known book extensions counts. Matches what scan
// callers will pass at runtime.
func supportedExt(loc string) bool {
	for _, ext := range []string{".epub", ".pdf", ".cbz", ".mp3", ".m4b", ".azw3", ".mobi", ".fb2"} {
		if strings.HasSuffix(loc, ext) {
			return true
		}
	}
	return false
}

func entries(locs ...string) []WalkEntry {
	out := make([]WalkEntry, len(locs))
	for i, l := range locs {
		out[i] = WalkEntry{Location: l, Size: 1}
	}
	return out
}

func leafFolders(c Classification) []string {
	out := make([]string, len(c.LeafBooks))
	for i, lb := range c.LeafBooks {
		out[i] = lb.Folder
	}
	return out
}

func flatLocs(c Classification) []string {
	out := make([]string, len(c.Flat))
	for i, e := range c.Flat {
		out[i] = e.Location
	}
	return out
}

func TestClassify_FlatRootFile(t *testing.T) {
	got := Classify(entries("hobbit.epub"), supportedExt)
	if len(got.LeafBooks) != 0 {
		t.Errorf("LeafBooks=%d want 0", len(got.LeafBooks))
	}
	if want := []string{"hobbit.epub"}; !equal(flatLocs(got), want) {
		t.Errorf("Flat=%v want %v", flatLocs(got), want)
	}
}

func TestClassify_PureLeafBook(t *testing.T) {
	got := Classify(entries(
		"Tolkien/Hobbit/hobbit.epub",
		"Tolkien/Hobbit/cover.jpg",
	), supportedExt)
	if len(got.LeafBooks) != 1 {
		t.Fatalf("LeafBooks=%d want 1", len(got.LeafBooks))
	}
	if got.LeafBooks[0].Folder != "Tolkien/Hobbit" {
		t.Errorf("Folder=%q", got.LeafBooks[0].Folder)
	}
	if got.LeafBooks[0].Mixed {
		t.Errorf("Mixed should be false")
	}
	// cover.jpg is not "supported" — not in Files.
	if len(got.LeafBooks[0].Files) != 1 {
		t.Errorf("Files=%d want 1", len(got.LeafBooks[0].Files))
	}
}

func TestClassify_LeafBookWithMultipleFormats(t *testing.T) {
	got := Classify(entries(
		"Tolkien/Hobbit/hobbit.epub",
		"Tolkien/Hobbit/hobbit.mp3",
	), supportedExt)
	if len(got.LeafBooks) != 1 {
		t.Fatalf("LeafBooks=%d want 1", len(got.LeafBooks))
	}
	if len(got.LeafBooks[0].Files) != 2 {
		t.Errorf("Files=%d want 2", len(got.LeafBooks[0].Files))
	}
}

func TestClassify_FlatChapterFolder(t *testing.T) {
	// Multi-chapter audiobook with all chapters as direct children of
	// the Book folder (no disc subdirs). Single LeafBook owning all
	// chapter files.
	got := Classify(entries(
		"Frank Herbert/Dune/ch01.mp3",
		"Frank Herbert/Dune/ch02.mp3",
		"Frank Herbert/Dune/ch03.mp3",
	), supportedExt)
	if len(got.LeafBooks) != 1 {
		t.Fatalf("LeafBooks=%d want 1, got %v", len(got.LeafBooks), leafFolders(got))
	}
	if got.LeafBooks[0].Folder != "Frank Herbert/Dune" {
		t.Errorf("Folder=%q", got.LeafBooks[0].Folder)
	}
	if got.LeafBooks[0].Mixed {
		t.Errorf("Mixed should be false")
	}
	if len(got.LeafBooks[0].Files) != 3 {
		t.Errorf("Files=%d want 3", len(got.LeafBooks[0].Files))
	}
}

func TestClassify_NestedDiscFolders(t *testing.T) {
	// Subdirs of an empty parent each hold their own files. Per spec,
	// each subdir is its own LeafBook (parent acts as a Container).
	got := Classify(entries(
		"Frank Herbert/Dune/disc1/ch01.mp3",
		"Frank Herbert/Dune/disc1/ch02.mp3",
		"Frank Herbert/Dune/disc2/ch03.mp3",
	), supportedExt)
	want := []string{"Frank Herbert/Dune/disc1", "Frank Herbert/Dune/disc2"}
	if !equal(leafFolders(got), want) {
		t.Errorf("folders=%v want %v", leafFolders(got), want)
	}
}

func TestClassify_ContainerWithNestedLeafBooks(t *testing.T) {
	// `Tolkien/` holds no supported files itself; subdirs do.
	got := Classify(entries(
		"Tolkien/Hobbit/hobbit.epub",
		"Tolkien/Silmarillion/silmarillion.epub",
	), supportedExt)
	want := []string{"Tolkien/Hobbit", "Tolkien/Silmarillion"}
	if !equal(leafFolders(got), want) {
		t.Errorf("LeafBook folders=%v want %v", leafFolders(got), want)
	}
}

func TestClassify_MixedDirectAndNested(t *testing.T) {
	// `Tolkien/` holds direct file AND a subdir with file.
	got := Classify(entries(
		"Tolkien/anthology.epub",
		"Tolkien/Hobbit/hobbit.epub",
	), supportedExt)
	if len(got.LeafBooks) != 2 {
		t.Fatalf("LeafBooks=%d want 2, got %v", len(got.LeafBooks), leafFolders(got))
	}
	want := []string{"Tolkien", "Tolkien/Hobbit"}
	if !equal(leafFolders(got), want) {
		t.Errorf("folders=%v want %v", leafFolders(got), want)
	}
	for _, lb := range got.LeafBooks {
		if lb.Folder == "Tolkien" {
			if !lb.Mixed {
				t.Errorf("Tolkien LeafBook should be Mixed=true")
			}
			if len(lb.Files) != 1 {
				t.Errorf("Tolkien Files=%d want 1 (depth-1 sweep)", len(lb.Files))
			}
		}
		if lb.Folder == "Tolkien/Hobbit" && lb.Mixed {
			t.Errorf("Tolkien/Hobbit Mixed=true should be false")
		}
	}
}

func TestClassify_FlatPlusLeafBooks(t *testing.T) {
	got := Classify(entries(
		"loose-book.epub",
		"Tolkien/Hobbit/hobbit.epub",
	), supportedExt)
	if want := []string{"loose-book.epub"}; !equal(flatLocs(got), want) {
		t.Errorf("Flat=%v want %v", flatLocs(got), want)
	}
	if want := []string{"Tolkien/Hobbit"}; !equal(leafFolders(got), want) {
		t.Errorf("LeafBooks=%v want %v", leafFolders(got), want)
	}
}

func TestClassify_OrphanSidecarDirIgnored(t *testing.T) {
	// A directory that holds only a non-supported file
	// (metadata.embookshelf.json) and no supported files in its
	// subtree should not produce a LeafBook.
	got := Classify(entries(
		"Tolkien/Hobbit/metadata.embookshelf.json",
	), supportedExt)
	if len(got.LeafBooks) != 0 {
		t.Errorf("LeafBooks=%d want 0 for orphan sidecar dir", len(got.LeafBooks))
	}
}

func TestClassify_DeterministicOrder(t *testing.T) {
	// Multiple LeafBooks should always come back in lex order.
	got := Classify(entries(
		"Z/last.epub",
		"A/first.epub",
		"M/middle.epub",
	), supportedExt)
	want := []string{"A", "M", "Z"}
	if !equal(leafFolders(got), want) {
		t.Errorf("order=%v want %v", leafFolders(got), want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
