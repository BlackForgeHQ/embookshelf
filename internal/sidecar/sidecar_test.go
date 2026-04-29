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
