package sidecar

import (
	"testing"
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
