// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"strings"
	"time"
)

// BookPatch is a partial edit to a Book: a nil field means "leave alone", a
// non-nil field means "set to this". It is the domain shape, deliberately
// separate from any HTTP wire type, so the editing rules in Apply hold for
// every caller rather than only for requests arriving at the metadata PATCH
// endpoint.
type BookPatch struct {
	Title         *string
	Subtitle      *string
	Author        *string
	Format        *string
	Year          *int
	PublishDate   *string
	Language      *string
	Rating        *int
	Palette       *string
	Description   *string
	ISBN          *string
	ISBN10        *string
	Publisher     *string
	Series        *string
	SeriesNum     *int
	SeriesTotal   *int
	Genres        *[]string
	Moods         *[]string
	Tags          *[]string
	AgeRating     *string
	ContentRating *string
	Pages         *int
	// PublicReviews is tri-state: nil leaves the book's setting alone, a
	// value sets it. PublicReviewsClear resets it to unset and wins over a
	// value, so a caller may send both and mean "clear".
	PublicReviews      *bool
	PublicReviewsClear bool
}

// PublishDateLayout is the wire format for a full publication date.
const PublishDateLayout = "2006-01-02"

// Apply writes the patch onto b, enforcing the book editing invariants:
//
//   - short text fields are trimmed; Description is kept verbatim
//   - Rating is clamped to 0–5, Pages and SeriesTotal to non-negative;
//     SeriesIndex is not clamped
//   - Genres, Moods and Tags are trimmed, emptied entries dropped, and
//     de-duplicated case-sensitively in first-occurrence order
//   - setting PublishDate also sets Year (see applyPublishDate)
//
// An unparseable PublishDate is ignored rather than rejected — the field
// keeps its previous value and no error is reported. Validating field
// *content* — a well-formed ISBN, a plausible year — happens nowhere on the
// server: ui/src/lib/metadata-validators.ts checks both in the browser only,
// so any non-UI caller can store a malformed ISBN or a year of 99999.
func (p BookPatch) Apply(b *Book) {
	if p.Title != nil {
		b.Title = strings.TrimSpace(*p.Title)
	}
	if p.Subtitle != nil {
		b.Subtitle = strings.TrimSpace(*p.Subtitle)
	}
	if p.Author != nil {
		b.Author = strings.TrimSpace(*p.Author)
	}
	if p.Format != nil {
		b.Format = strings.TrimSpace(*p.Format)
	}
	if p.Year != nil {
		b.Year = *p.Year
	}
	if p.PublishDate != nil {
		p.applyPublishDate(b)
	}
	if p.Language != nil {
		b.Language = strings.TrimSpace(*p.Language)
	}
	if p.Rating != nil {
		b.Rating = clampInt(*p.Rating, 0, 5)
	}
	if p.Palette != nil {
		b.CoverPalette = strings.TrimSpace(*p.Palette)
	}
	if p.Description != nil {
		b.Description = *p.Description
	}
	if p.ISBN != nil {
		b.ISBN = strings.TrimSpace(*p.ISBN)
	}
	if p.ISBN10 != nil {
		b.ISBN10 = strings.TrimSpace(*p.ISBN10)
	}
	if p.Publisher != nil {
		b.Publisher = strings.TrimSpace(*p.Publisher)
	}
	if p.Series != nil {
		b.Series = strings.TrimSpace(*p.Series)
	}
	if p.SeriesNum != nil {
		b.SeriesIndex = *p.SeriesNum
	}
	if p.SeriesTotal != nil {
		b.SeriesTotal = atLeastZero(*p.SeriesTotal)
	}
	if p.Genres != nil {
		b.Genres = CleanStringSlice(*p.Genres)
	}
	if p.Moods != nil {
		b.Moods = CleanStringSlice(*p.Moods)
	}
	if p.Tags != nil {
		b.Tags = CleanStringSlice(*p.Tags)
	}
	if p.AgeRating != nil {
		b.AgeRating = strings.TrimSpace(*p.AgeRating)
	}
	if p.ContentRating != nil {
		b.ContentRating = strings.TrimSpace(*p.ContentRating)
	}
	if p.Pages != nil {
		b.Pages = atLeastZero(*p.Pages)
	}
	if p.PublicReviewsClear {
		b.PublicReviews = nil
	} else if p.PublicReviews != nil {
		v := *p.PublicReviews
		b.PublicReviews = &v
	}
}

// applyPublishDate sets the full date and, when one parses, drags Year along
// with it.
//
// The coupling is deliberate but easy to miss from the call site: Book.Year
// is a denormalised sort and display column, and leaving it stale after a
// date edit shows two different years for the same book. A patch carrying
// both PublishDate and Year therefore ends up with the date's year — Year is
// assigned first and then overwritten here.
//
// An empty string clears the date but leaves Year alone; there is nothing to
// derive a new one from, and clearing it would lose data the user did not ask
// to lose. An unparseable string is ignored entirely.
func (p BookPatch) applyPublishDate(b *Book) {
	raw := strings.TrimSpace(*p.PublishDate)
	if raw == "" {
		b.PublishDate = nil
		return
	}
	t, err := time.Parse(PublishDateLayout, raw)
	if err != nil {
		return
	}
	b.PublishDate = &t
	b.Year = t.Year()
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func atLeastZero(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// CleanStringSlice trims each entry, drops the ones left empty, and removes
// duplicates in first-occurrence order. Comparison is case-sensitive,
// matching DedupTags — "Sci-Fi" and "sci-fi" are different tags here.
//
// Trimming happens before de-duplication so that " dark " and "dark" collapse
// into one entry, and the result is a fresh slice so the caller's input is
// never written through.
func CleanStringSlice(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
