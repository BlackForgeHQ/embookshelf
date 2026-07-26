// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// TestBookPatchApplyLeavesNilFieldsAlone is the whole premise of a patch:
// a nil field must not touch the book.
func TestBookPatchApplyLeavesNilFieldsAlone(t *testing.T) {
	before := Book{
		Title:         "Dune",
		Subtitle:      "Book One",
		Author:        "Frank Herbert",
		Format:        "EPUB",
		Year:          1965,
		Language:      "en",
		Rating:        4,
		CoverPalette:  "#c1440e",
		Description:   "Desert planet.",
		ISBN:          "9780441013593",
		ISBN10:        "0441013597",
		Publisher:     "Chilton",
		Series:        "Dune",
		SeriesIndex:   1,
		SeriesTotal:   6,
		Genres:        []string{"sci-fi"},
		Moods:         []string{"epic"},
		Tags:          []string{"classic"},
		AgeRating:     "teen",
		ContentRating: "pg13",
		Pages:         412,
		PublicReviews: ptr(true),
	}
	got := before
	BookPatch{}.Apply(&got)

	if got.Title != before.Title || got.Rating != before.Rating || got.Pages != before.Pages {
		t.Fatalf("empty patch modified scalars: %+v", got)
	}
	if got.PublicReviews == nil || *got.PublicReviews != true {
		t.Fatalf("empty patch modified PublicReviews: %v", got.PublicReviews)
	}
	if len(got.Genres) != 1 || len(got.Moods) != 1 || len(got.Tags) != 1 {
		t.Fatalf("empty patch modified slices: %+v", got)
	}
}

// TestBookPatchApplySetsEveryField gives each field one distinct value so a
// mis-wired assignment (Subtitle written from Title, say) is caught.
func TestBookPatchApplySetsEveryField(t *testing.T) {
	var b Book
	BookPatch{
		Title:         ptr("Title-v"),
		Subtitle:      ptr("Subtitle-v"),
		Author:        ptr("Author-v"),
		Format:        ptr("Format-v"),
		Year:          ptr(1911),
		Language:      ptr("Language-v"),
		Rating:        ptr(3),
		Palette:       ptr("Palette-v"),
		Description:   ptr("Description-v"),
		ISBN:          ptr("ISBN-v"),
		ISBN10:        ptr("ISBN10-v"),
		Publisher:     ptr("Publisher-v"),
		Series:        ptr("Series-v"),
		SeriesNum:     ptr(7),
		SeriesTotal:   ptr(9),
		Genres:        ptr([]string{"Genres-v"}),
		Moods:         ptr([]string{"Moods-v"}),
		Tags:          ptr([]string{"Tags-v"}),
		AgeRating:     ptr("AgeRating-v"),
		ContentRating: ptr("ContentRating-v"),
		Pages:         ptr(321),
		PublicReviews: ptr(true),
	}.Apply(&b)

	checks := []struct {
		field string
		got   string
		want  string
	}{
		{"Title", b.Title, "Title-v"},
		{"Subtitle", b.Subtitle, "Subtitle-v"},
		{"Author", b.Author, "Author-v"},
		{"Format", b.Format, "Format-v"},
		{"Language", b.Language, "Language-v"},
		{"CoverPalette", b.CoverPalette, "Palette-v"},
		{"Description", b.Description, "Description-v"},
		{"ISBN", b.ISBN, "ISBN-v"},
		{"ISBN10", b.ISBN10, "ISBN10-v"},
		{"Publisher", b.Publisher, "Publisher-v"},
		{"Series", b.Series, "Series-v"},
		{"AgeRating", b.AgeRating, "AgeRating-v"},
		{"ContentRating", b.ContentRating, "ContentRating-v"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	if b.Year != 1911 {
		t.Errorf("Year = %d, want 1911", b.Year)
	}
	if b.Rating != 3 {
		t.Errorf("Rating = %d, want 3", b.Rating)
	}
	if b.SeriesIndex != 7 {
		t.Errorf("SeriesIndex = %d, want 7", b.SeriesIndex)
	}
	if b.SeriesTotal != 9 {
		t.Errorf("SeriesTotal = %d, want 9", b.SeriesTotal)
	}
	if b.Pages != 321 {
		t.Errorf("Pages = %d, want 321", b.Pages)
	}
	if len(b.Genres) != 1 || b.Genres[0] != "Genres-v" {
		t.Errorf("Genres = %v", b.Genres)
	}
	if len(b.Moods) != 1 || b.Moods[0] != "Moods-v" {
		t.Errorf("Moods = %v", b.Moods)
	}
	if len(b.Tags) != 1 || b.Tags[0] != "Tags-v" {
		t.Errorf("Tags = %v", b.Tags)
	}
	if b.PublicReviews == nil || !*b.PublicReviews {
		t.Errorf("PublicReviews = %v, want true", b.PublicReviews)
	}
}

func TestBookPatchApplyTrimsTextFields(t *testing.T) {
	var b Book
	BookPatch{
		Title:         ptr("  Dune  "),
		Subtitle:      ptr("\tBook One\n"),
		Author:        ptr(" Frank Herbert "),
		Format:        ptr(" EPUB "),
		Language:      ptr(" en "),
		Palette:       ptr(" #c1440e "),
		ISBN:          ptr(" 9780441013593 "),
		ISBN10:        ptr(" 0441013597 "),
		Publisher:     ptr(" Chilton "),
		Series:        ptr(" Dune "),
		AgeRating:     ptr(" teen "),
		ContentRating: ptr(" pg13 "),
	}.Apply(&b)

	for field, got := range map[string]string{
		"Title": b.Title, "Subtitle": b.Subtitle, "Author": b.Author,
		"Format": b.Format, "Language": b.Language, "CoverPalette": b.CoverPalette,
		"ISBN": b.ISBN, "ISBN10": b.ISBN10, "Publisher": b.Publisher,
		"Series": b.Series, "AgeRating": b.AgeRating, "ContentRating": b.ContentRating,
	} {
		if got != trimmed(got) {
			t.Errorf("%s = %q, still has surrounding space", field, got)
		}
	}
	if b.Title != "Dune" || b.Subtitle != "Book One" || b.ContentRating != "pg13" {
		t.Fatalf("trim wrong: %q / %q / %q", b.Title, b.Subtitle, b.ContentRating)
	}
}

func trimmed(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}

// TestBookPatchApplyKeepsDescriptionVerbatim — description is prose and may
// legitimately open or close with whitespace, unlike the short fields.
func TestBookPatchApplyKeepsDescriptionVerbatim(t *testing.T) {
	var b Book
	BookPatch{Description: ptr("  leading and trailing  ")}.Apply(&b)
	if b.Description != "  leading and trailing  " {
		t.Fatalf("Description = %q, want it untrimmed", b.Description)
	}
}

func TestBookPatchApplyClampsRating(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{-7, 0}, {-1, 0}, {0, 0}, {3, 3}, {5, 5}, {6, 5}, {900, 5},
	} {
		b := Book{Rating: 2}
		BookPatch{Rating: ptr(tc.in)}.Apply(&b)
		if b.Rating != tc.want {
			t.Errorf("Rating(%d) = %d, want %d", tc.in, b.Rating, tc.want)
		}
	}
}

func TestBookPatchApplyClampsCountsToNonNegative(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{-5, 0}, {-1, 0}, {0, 0}, {412, 412}} {
		b := Book{Pages: 1, SeriesTotal: 1}
		BookPatch{Pages: ptr(tc.in), SeriesTotal: ptr(tc.in)}.Apply(&b)
		if b.Pages != tc.want {
			t.Errorf("Pages(%d) = %d, want %d", tc.in, b.Pages, tc.want)
		}
		if b.SeriesTotal != tc.want {
			t.Errorf("SeriesTotal(%d) = %d, want %d", tc.in, b.SeriesTotal, tc.want)
		}
	}
}

// TestBookPatchApplyAllowsNegativeSeriesIndex documents that SeriesIndex is
// NOT clamped while SeriesTotal is — a real asymmetry, not an oversight in
// the test.
func TestBookPatchApplyAllowsNegativeSeriesIndex(t *testing.T) {
	var b Book
	BookPatch{SeriesNum: ptr(-3)}.Apply(&b)
	if b.SeriesIndex != -3 {
		t.Fatalf("SeriesIndex = %d, want -3 (unclamped)", b.SeriesIndex)
	}
}

func TestBookPatchApplyCleansCategorySlices(t *testing.T) {
	var b Book
	BookPatch{
		Genres: ptr([]string{" sci-fi ", "", "sci-fi", "  ", "epic"}),
		Moods:  ptr([]string{"dark", "dark", " dark "}),
		Tags:   ptr([]string{"  ", ""}),
	}.Apply(&b)

	if got := b.Genres; len(got) != 2 || got[0] != "sci-fi" || got[1] != "epic" {
		t.Errorf("Genres = %#v, want [sci-fi epic]", got)
	}
	// " dark " trims to "dark", so it must collapse into the earlier entry:
	// trimming has to happen before de-duplication, not after.
	if got := b.Moods; len(got) != 1 || got[0] != "dark" {
		t.Errorf("Moods = %#v, want [dark]", got)
	}
	if len(b.Tags) != 0 {
		t.Errorf("Tags = %#v, want empty", b.Tags)
	}
}

// TestBookPatchApplyDedupeIsCaseSensitive pins the codebase convention set
// by DedupTags: "Sci-Fi" and "sci-fi" are different tags.
func TestBookPatchApplyDedupeIsCaseSensitive(t *testing.T) {
	var b Book
	BookPatch{Tags: ptr([]string{"Sci-Fi", "sci-fi"})}.Apply(&b)
	if len(b.Tags) != 2 {
		t.Fatalf("Tags = %#v, want both cases kept", b.Tags)
	}
}

// TestBookPatchApplyDoesNotAliasInputSlice — Apply must not write through to
// the caller's backing array.
func TestBookPatchApplyDoesNotAliasInputSlice(t *testing.T) {
	in := []string{"keep", "", "also"}
	var b Book
	BookPatch{Tags: ptr(in)}.Apply(&b)
	if in[0] != "keep" || in[1] != "" || in[2] != "also" {
		t.Fatalf("input slice mutated: %#v", in)
	}
}

func TestBookPatchApplySetsPublishDate(t *testing.T) {
	var b Book
	BookPatch{PublishDate: ptr(" 1965-08-01 ")}.Apply(&b)
	if b.PublishDate == nil {
		t.Fatal("PublishDate nil, want parsed")
	}
	if got := b.PublishDate.Format("2006-01-02"); got != "1965-08-01" {
		t.Fatalf("PublishDate = %s", got)
	}
}

// TestBookPatchApplyPublishDateDragsYear pins the hidden coupling: books.year
// is a denormalised display column, and a full date edit moves it too.
func TestBookPatchApplyPublishDateDragsYear(t *testing.T) {
	b := Book{Year: 1900}
	BookPatch{PublishDate: ptr("1965-08-01")}.Apply(&b)
	if b.Year != 1965 {
		t.Fatalf("Year = %d, want 1965 (dragged by PublishDate)", b.Year)
	}
}

// TestBookPatchApplyPublishDateBeatsExplicitYear — a patch carrying both wins
// with the date's year. Callers that mean to set Year independently must not
// send PublishDate in the same patch.
func TestBookPatchApplyPublishDateBeatsExplicitYear(t *testing.T) {
	var b Book
	BookPatch{Year: ptr(1900), PublishDate: ptr("1965-08-01")}.Apply(&b)
	if b.Year != 1965 {
		t.Fatalf("Year = %d, want 1965 (date wins over explicit year)", b.Year)
	}
}

func TestBookPatchApplyEmptyPublishDateClearsIt(t *testing.T) {
	when := time.Date(1965, 8, 1, 0, 0, 0, 0, time.UTC)
	b := Book{PublishDate: &when, Year: 1965}
	BookPatch{PublishDate: ptr("   ")}.Apply(&b)
	if b.PublishDate != nil {
		t.Fatalf("PublishDate = %v, want nil", b.PublishDate)
	}
	if b.Year != 1965 {
		t.Fatalf("Year = %d, want 1965 — clearing the date must not clear the year", b.Year)
	}
}

// TestBookPatchApplyIgnoresUnparseablePublishDate — the server accepts the
// request and silently keeps the old value rather than erroring.
func TestBookPatchApplyIgnoresUnparseablePublishDate(t *testing.T) {
	when := time.Date(1965, 8, 1, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{"nonsense", "1965", "08/01/1965", "1965-13-45"} {
		b := Book{PublishDate: &when, Year: 1965}
		BookPatch{PublishDate: ptr(raw)}.Apply(&b)
		if b.PublishDate == nil || !b.PublishDate.Equal(when) {
			t.Errorf("%q: PublishDate = %v, want unchanged", raw, b.PublishDate)
		}
		if b.Year != 1965 {
			t.Errorf("%q: Year = %d, want unchanged", raw, b.Year)
		}
	}
}

func TestBookPatchApplyPublicReviewsTriState(t *testing.T) {
	tests := []struct {
		name  string
		start *bool
		patch BookPatch
		want  *bool
	}{
		{"unset stays unset", nil, BookPatch{}, nil},
		{"set true", nil, BookPatch{PublicReviews: ptr(true)}, ptr(true)},
		{"set false", ptr(true), BookPatch{PublicReviews: ptr(false)}, ptr(false)},
		{"clear wins over set", ptr(false), BookPatch{PublicReviews: ptr(true), PublicReviewsClear: true}, nil},
		{"clear alone", ptr(true), BookPatch{PublicReviewsClear: true}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := Book{PublicReviews: tc.start}
			tc.patch.Apply(&b)
			switch {
			case tc.want == nil && b.PublicReviews != nil:
				t.Fatalf("PublicReviews = %v, want nil", *b.PublicReviews)
			case tc.want != nil && b.PublicReviews == nil:
				t.Fatalf("PublicReviews = nil, want %v", *tc.want)
			case tc.want != nil && *b.PublicReviews != *tc.want:
				t.Fatalf("PublicReviews = %v, want %v", *b.PublicReviews, *tc.want)
			}
		})
	}
}

// TestBookPatchApplyCopiesPublicReviews — the book must not end up sharing a
// bool with the patch, or a later write through the patch would mutate it.
func TestBookPatchApplyCopiesPublicReviews(t *testing.T) {
	v := true
	var b Book
	BookPatch{PublicReviews: &v}.Apply(&b)
	if b.PublicReviews == &v {
		t.Fatal("book aliases the patch's bool")
	}
}
