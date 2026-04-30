package sidecar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/blackforge/embookshelf/internal/storage"
	"github.com/blackforge/embookshelf/internal/storage/local"
)

// ---- TOML round-trip tests ----

func TestTOML_RoundTrip(t *testing.T) {
	original := Sidecar{
		Title:         "The Great Gatsby",
		TitleSort:     "Great Gatsby, The",
		Subtitle:      "A Novel",
		Author:        "F. Scott Fitzgerald",
		Description:   "A story of the Jazz Age.",
		Language:      "en",
		Publisher:     "Scribner",
		PublishedDate: "1925-04-10",
		ISBN:          "978-0-7432-7356-5",
		Series:        "American Classics",
		SeriesIndex:   3,
		Tags:          []string{"fiction", "classic"},
		Genres:        []string{"literary fiction"},
	}

	data, err := EncodeTOML(original)
	if err != nil {
		t.Fatalf("EncodeTOML: %v", err)
	}

	parsed, err := ParseTOML(data)
	if err != nil {
		t.Fatalf("ParseTOML: %v", err)
	}

	if parsed.Title != original.Title {
		t.Errorf("Title: got %q want %q", parsed.Title, original.Title)
	}
	if parsed.TitleSort != original.TitleSort {
		t.Errorf("TitleSort: got %q want %q", parsed.TitleSort, original.TitleSort)
	}
	if parsed.Subtitle != original.Subtitle {
		t.Errorf("Subtitle: got %q want %q", parsed.Subtitle, original.Subtitle)
	}
	if parsed.Author != original.Author {
		t.Errorf("Author: got %q want %q", parsed.Author, original.Author)
	}
	if parsed.Description != original.Description {
		t.Errorf("Description: got %q want %q", parsed.Description, original.Description)
	}
	if parsed.Language != original.Language {
		t.Errorf("Language: got %q want %q", parsed.Language, original.Language)
	}
	if parsed.Publisher != original.Publisher {
		t.Errorf("Publisher: got %q want %q", parsed.Publisher, original.Publisher)
	}
	if parsed.PublishedDate != original.PublishedDate {
		t.Errorf("PublishedDate: got %q want %q", parsed.PublishedDate, original.PublishedDate)
	}
	if parsed.ISBN != original.ISBN {
		t.Errorf("ISBN: got %q want %q", parsed.ISBN, original.ISBN)
	}
	if parsed.Series != original.Series {
		t.Errorf("Series: got %q want %q", parsed.Series, original.Series)
	}
	if parsed.SeriesIndex != original.SeriesIndex {
		t.Errorf("SeriesIndex: got %d want %d", parsed.SeriesIndex, original.SeriesIndex)
	}
	if len(parsed.Tags) != len(original.Tags) {
		t.Errorf("Tags len: got %d want %d", len(parsed.Tags), len(original.Tags))
	} else {
		for i, tag := range original.Tags {
			if parsed.Tags[i] != tag {
				t.Errorf("Tags[%d]: got %q want %q", i, parsed.Tags[i], tag)
			}
		}
	}
	if len(parsed.Genres) != len(original.Genres) {
		t.Errorf("Genres len: got %d want %d", len(parsed.Genres), len(original.Genres))
	} else {
		for i, g := range original.Genres {
			if parsed.Genres[i] != g {
				t.Errorf("Genres[%d]: got %q want %q", i, parsed.Genres[i], g)
			}
		}
	}
}

func TestParseTOML_MalformedReturnsError(t *testing.T) {
	_, err := ParseTOML([]byte("not valid toml [[["))
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

// ---- IsZero tests ----

func TestIsZero_EmptySidecar(t *testing.T) {
	empty := Sidecar{}
	if !empty.IsZero() {
		t.Fatal("empty Sidecar should be zero")
	}
}

func TestIsZero_AfterFieldSet(t *testing.T) {
	tests := []struct {
		name string
		s    Sidecar
	}{
		{"Title", Sidecar{Title: "x"}},
		{"Author", Sidecar{Author: "x"}},
		{"TitleSort", Sidecar{TitleSort: "x"}},
		{"Subtitle", Sidecar{Subtitle: "x"}},
		{"Description", Sidecar{Description: "x"}},
		{"Language", Sidecar{Language: "x"}},
		{"Publisher", Sidecar{Publisher: "x"}},
		{"PublishedDate", Sidecar{PublishedDate: "x"}},
		{"ISBN", Sidecar{ISBN: "x"}},
		{"Series", Sidecar{Series: "x"}},
		{"SeriesIndex", Sidecar{SeriesIndex: 1}},
		{"Tags", Sidecar{Tags: []string{"x"}}},
		{"Genres", Sidecar{Genres: []string{"x"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.s.IsZero() {
				t.Fatalf("Sidecar with %s set should not be zero", tc.name)
			}
		})
	}
}

// ---- Merge tests ----

func TestMerge_EmptyOverSet_AWins(t *testing.T) {
	a := Sidecar{Title: "Original", Author: "Author A"}
	b := Sidecar{} // empty
	out := Merge(a, b)
	if out.Title != "Original" {
		t.Errorf("Title: got %q want %q", out.Title, "Original")
	}
	if out.Author != "Author A" {
		t.Errorf("Author: got %q want %q", out.Author, "Author A")
	}
}

func TestMerge_SetOverEmpty_BWins(t *testing.T) {
	a := Sidecar{} // empty
	b := Sidecar{Title: "Override", Author: "Author B"}
	out := Merge(a, b)
	if out.Title != "Override" {
		t.Errorf("Title: got %q want %q", out.Title, "Override")
	}
	if out.Author != "Author B" {
		t.Errorf("Author: got %q want %q", out.Author, "Author B")
	}
}

func TestMerge_SetOverSet_BWins(t *testing.T) {
	a := Sidecar{Title: "Original", Author: "Author A"}
	b := Sidecar{Title: "Override", Author: "Author B"}
	out := Merge(a, b)
	if out.Title != "Override" {
		t.Errorf("Title: got %q want %q", out.Title, "Override")
	}
	if out.Author != "Author B" {
		t.Errorf("Author: got %q want %q", out.Author, "Author B")
	}
}

func TestMerge_EmptySlicesInBDontClobberA(t *testing.T) {
	a := Sidecar{Tags: []string{"fiction", "classic"}, Genres: []string{"literary"}}
	b := Sidecar{Title: "Override"} // empty Tags and Genres
	out := Merge(a, b)
	if len(out.Tags) != 2 {
		t.Errorf("Tags: got %v want [fiction classic]", out.Tags)
	}
	if len(out.Genres) != 1 {
		t.Errorf("Genres: got %v want [literary]", out.Genres)
	}
	if out.Title != "Override" {
		t.Errorf("Title: got %q want Override", out.Title)
	}
}

// ---- OPF parser tests ----

const calibreOPF = `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Dune</dc:title>
    <dc:creator opf:role="aut">Frank Herbert</dc:creator>
    <dc:description>A science fiction epic about desert planet.</dc:description>
    <dc:language>en</dc:language>
    <dc:publisher>Chilton Books</dc:publisher>
    <dc:date>1965-08-01</dc:date>
    <dc:identifier opf:scheme="ISBN">978-0-441-17271-9</dc:identifier>
    <dc:subject>Science Fiction</dc:subject>
    <dc:subject>Classic</dc:subject>
    <meta name="calibre:series" content="Dune Chronicles"/>
    <meta name="calibre:series_index" content="1"/>
    <meta name="calibre:title_sort" content="Dune"/>
  </metadata>
</package>`

func TestParseOPF_CalibreOPF(t *testing.T) {
	s, err := ParseOPF([]byte(calibreOPF))
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}
	if s.Title != "Dune" {
		t.Errorf("Title: got %q want %q", s.Title, "Dune")
	}
	if s.Author != "Frank Herbert" {
		t.Errorf("Author: got %q want %q", s.Author, "Frank Herbert")
	}
	if s.Description != "A science fiction epic about desert planet." {
		t.Errorf("Description: got %q", s.Description)
	}
	if s.Language != "en" {
		t.Errorf("Language: got %q want en", s.Language)
	}
	if s.Publisher != "Chilton Books" {
		t.Errorf("Publisher: got %q want Chilton Books", s.Publisher)
	}
	if s.PublishedDate != "1965-08-01" {
		t.Errorf("PublishedDate: got %q want 1965-08-01", s.PublishedDate)
	}
	if s.ISBN != "978-0-441-17271-9" {
		t.Errorf("ISBN: got %q want 978-0-441-17271-9", s.ISBN)
	}
	if len(s.Tags) != 2 || s.Tags[0] != "Science Fiction" || s.Tags[1] != "Classic" {
		t.Errorf("Tags: got %v want [Science Fiction Classic]", s.Tags)
	}
	if s.Series != "Dune Chronicles" {
		t.Errorf("Series: got %q want Dune Chronicles", s.Series)
	}
	if s.SeriesIndex != 1 {
		t.Errorf("SeriesIndex: got %d want 1", s.SeriesIndex)
	}
	if s.TitleSort != "Dune" {
		t.Errorf("TitleSort: got %q want Dune", s.TitleSort)
	}
}

func TestParseOPF_MultipleCreators_RoleAutWins(t *testing.T) {
	opf := `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Test Book</dc:title>
    <dc:creator opf:role="edt">Editor Person</dc:creator>
    <dc:creator opf:role="aut">Real Author</dc:creator>
    <dc:creator>Just A Name</dc:creator>
  </metadata>
</package>`
	s, err := ParseOPF([]byte(opf))
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}
	if s.Author != "Real Author" {
		t.Errorf("Author: got %q want Real Author (role=aut should win)", s.Author)
	}
}

func TestParseOPF_MultipleCreators_NoRoleUsesFirst(t *testing.T) {
	opf := `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Test Book</dc:title>
    <dc:creator opf:role="edt">Editor Person</dc:creator>
    <dc:creator opf:role="ill">Illustrator</dc:creator>
  </metadata>
</package>`
	s, err := ParseOPF([]byte(opf))
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}
	// No aut role — first entry wins
	if s.Author != "Editor Person" {
		t.Errorf("Author: got %q want Editor Person (first entry)", s.Author)
	}
}

func TestParseOPF_ISBNSchemeCasing(t *testing.T) {
	tests := []struct {
		scheme string
	}{
		{"ISBN"},
		{"isbn"},
		{"ISBN-13"},
	}
	for _, tc := range tests {
		t.Run(tc.scheme, func(t *testing.T) {
			opf := `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Book</dc:title>
    <dc:identifier opf:scheme="` + tc.scheme + `">978-0-000-00000-0</dc:identifier>
  </metadata>
</package>`
			s, err := ParseOPF([]byte(opf))
			if err != nil {
				t.Fatalf("ParseOPF: %v", err)
			}
			if s.ISBN != "978-0-000-00000-0" {
				t.Errorf("ISBN scheme=%q: got %q want 978-0-000-00000-0", tc.scheme, s.ISBN)
			}
		})
	}
}

func TestParseOPF_CalibreMeta(t *testing.T) {
	opf := `<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>Foundation</dc:title>
    <meta name="calibre:series" content="Foundation Series"/>
    <meta name="calibre:series_index" content="2"/>
    <meta name="calibre:title_sort" content="Foundation"/>
  </metadata>
</package>`
	s, err := ParseOPF([]byte(opf))
	if err != nil {
		t.Fatalf("ParseOPF: %v", err)
	}
	if s.Series != "Foundation Series" {
		t.Errorf("Series: got %q want Foundation Series", s.Series)
	}
	if s.SeriesIndex != 2 {
		t.Errorf("SeriesIndex: got %d want 2", s.SeriesIndex)
	}
	if s.TitleSort != "Foundation" {
		t.Errorf("TitleSort: got %q want Foundation", s.TitleSort)
	}
}

func TestParseOPF_MalformedXMLReturnsError(t *testing.T) {
	_, err := ParseOPF([]byte("<package><metadata><unclosed>"))
	if err == nil {
		t.Fatal("expected error for malformed XML, got nil")
	}
}

// ---- Writer tests ----

func newTestStore(t *testing.T) *local.LocalFS {
	t.Helper()
	fs, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("local.New: %v", err)
	}
	return fs
}

func TestWriter_SingleWriteHappyPath(t *testing.T) {
	store := newTestStore(t)
	w := NewWriter()
	ctx := context.Background()

	sc := Sidecar{Title: "Hello", Author: "World"}
	if err := w.Write(ctx, store, "book.embookshelf.toml", sc); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := store.Get(ctx, "book.embookshelf.toml")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	parsed, err := ParseTOML(data)
	if err != nil {
		t.Fatalf("ParseTOML: %v", err)
	}
	if parsed.Title != "Hello" || parsed.Author != "World" {
		t.Errorf("parsed mismatch: got %+v", parsed)
	}
}

func TestWriter_ConcurrentWritesSameKey(t *testing.T) {
	store := newTestStore(t)
	w := NewWriter()
	ctx := context.Background()
	key := "concurrent.embookshelf.toml"

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			sc := Sidecar{Title: fmt.Sprintf("Title-%d", i)}
			if err := w.Write(ctx, store, key, sc); err != nil {
				t.Errorf("Write[%d]: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// After all writes, the key must be parsable (no torn writes).
	rc, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = rc.Close() }()
	data, _ := io.ReadAll(rc)
	if _, err := ParseTOML(data); err != nil {
		t.Fatalf("final read not parseable: %v", err)
	}
}

func TestWriter_ConcurrentWritesDistinctKeys(t *testing.T) {
	store := newTestStore(t)
	w := NewWriter()
	ctx := context.Background()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("book%d/.embookshelf.toml", i)
			sc := Sidecar{Title: fmt.Sprintf("Title-%d", i)}
			if err := w.Write(ctx, store, key, sc); err != nil {
				t.Errorf("Write[%d]: %v", i, err)
			}
		}()
	}
	wg.Wait()

	// Verify each key has its own content.
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("book%d/.embookshelf.toml", i)
		rc, err := store.Get(ctx, key)
		if err != nil {
			t.Errorf("Get[%d]: %v", i, err)
			continue
		}
		data, readErr := io.ReadAll(rc)
		_ = rc.Close()
		if readErr != nil {
			t.Errorf("ReadAll[%d]: %v", i, readErr)
			continue
		}
		parsed, parseErr := ParseTOML(data)
		if parseErr != nil {
			t.Errorf("ParseTOML[%d]: %v", i, parseErr)
			continue
		}
		expected := fmt.Sprintf("Title-%d", i)
		if parsed.Title != expected {
			t.Errorf("key %s: title got %q want %q", key, parsed.Title, expected)
		}
	}
}

// ---- Reader tests ----

// putFile is a helper that writes content to key in the store.
func putFile(t *testing.T, store storage.Storage, key string, content []byte) {
	t.Helper()
	_, err := store.Put(context.Background(), key, bytes.NewReader(content), storage.WithContentType("text/plain"))
	if err != nil {
		t.Fatalf("putFile %q: %v", key, err)
	}
}

func TestRead_NoSidecars(t *testing.T) {
	store := newTestStore(t)
	s, err := Read(context.Background(), store, "books/mybook")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !s.IsZero() {
		t.Errorf("expected zero Sidecar when no sidecars, got %+v", s)
	}
}

func TestRead_OnlyOPF(t *testing.T) {
	store := newTestStore(t)
	opf := []byte(`<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>OPF Title</dc:title>
    <dc:creator opf:role="aut">OPF Author</dc:creator>
    <dc:language>en</dc:language>
  </metadata>
</package>`)
	putFile(t, store, "books/mybook/metadata.opf", opf)

	s, err := Read(context.Background(), store, "books/mybook")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Title != "OPF Title" {
		t.Errorf("Title: got %q want OPF Title", s.Title)
	}
	if s.Author != "OPF Author" {
		t.Errorf("Author: got %q want OPF Author", s.Author)
	}
}

func TestRead_OnlyTOML(t *testing.T) {
	store := newTestStore(t)
	tomlContent := []byte(`title = "TOML Title"
author = "TOML Author"
language = "fr"
`)
	putFile(t, store, "books/mybook/.embookshelf.toml", tomlContent)

	s, err := Read(context.Background(), store, "books/mybook")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if s.Title != "TOML Title" {
		t.Errorf("Title: got %q want TOML Title", s.Title)
	}
	if s.Author != "TOML Author" {
		t.Errorf("Author: got %q want TOML Author", s.Author)
	}
}

func TestRead_BothPresent_TOMLWinsOnOverlap(t *testing.T) {
	store := newTestStore(t)
	opf := []byte(`<?xml version='1.0' encoding='utf-8'?>
<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>OPF Title</dc:title>
    <dc:creator opf:role="aut">OPF Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:publisher>OPF Publisher</dc:publisher>
  </metadata>
</package>`)
	tomlContent := []byte(`title = "TOML Title"
author = "TOML Author"
`)
	putFile(t, store, "books/mybook/metadata.opf", opf)
	putFile(t, store, "books/mybook/.embookshelf.toml", tomlContent)

	s, err := Read(context.Background(), store, "books/mybook")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// TOML wins on overlapping fields
	if s.Title != "TOML Title" {
		t.Errorf("Title: got %q want TOML Title (TOML should win)", s.Title)
	}
	if s.Author != "TOML Author" {
		t.Errorf("Author: got %q want TOML Author (TOML should win)", s.Author)
	}
	// OPF fills fields absent from TOML
	if s.Publisher != "OPF Publisher" {
		t.Errorf("Publisher: got %q want OPF Publisher (OPF fills remainder)", s.Publisher)
	}
}

func TestRead_MalformedTOML_ReturnsError(t *testing.T) {
	store := newTestStore(t)
	putFile(t, store, "books/mybook/.embookshelf.toml", []byte("not valid toml [[["))

	_, err := Read(context.Background(), store, "books/mybook")
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

func TestWriteMode_String(t *testing.T) {
	if got := string(ModeSpillover); got != "spillover" {
		t.Errorf("ModeSpillover = %q, want %q", got, "spillover")
	}
	if got := string(ModeFull); got != "full" {
		t.Errorf("ModeFull = %q, want %q", got, "full")
	}
}
