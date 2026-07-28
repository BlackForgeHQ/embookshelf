// SPDX-License-Identifier: AGPL-3.0-or-later

package model

// BookLocks is the per-field lock set for a book. Each flag corresponds
// to a `<field>_locked` column on the books table; when set, the
// apply-metadata flow (provider fan-out → user-selected match → PUT
// /books/:id/metadata) leaves that field alone even if the candidate
// carries a value.
//
// The struct is the storage; LockSpecs below is the vocabulary. Callers
// that need to enumerate locks go through the spec list, not through
// these fields — a field read directly is a projection nothing checks.
type BookLocks struct {
	Title       bool
	Subtitle    bool
	Author      bool
	Description bool
	Publisher   bool
	Series      bool
	ISBN        bool
	ISBN10      bool
	Language    bool
	PublishDate bool
	Genres      bool
	Moods       bool
	Tags        bool
	Pages       bool
	Cover       bool
}

// LockField is the wire name of one per-field lock. It is a defined type
// rather than a bare string so that every projection names a constant:
// renaming a lock is then a compile error at each site instead of a
// literal that silently stops matching.
type LockField string

// The lock vocabulary. These constants are the only legal LockField
// values; LockSpecs below is the single declaration that gives each one a
// database column and a flag on BookLocks.
const (
	LockTitle       LockField = "title"
	LockSubtitle    LockField = "subtitle"
	LockAuthor      LockField = "author"
	LockDescription LockField = "description"
	LockPublisher   LockField = "publisher"
	LockSeries      LockField = "series"
	LockISBN        LockField = "isbn"
	LockISBN10      LockField = "isbn10"
	LockLanguage    LockField = "language"
	LockPublishDate LockField = "publishDate"
	LockGenres      LockField = "genres"
	LockMoods       LockField = "moods"
	LockTags        LockField = "tags"
	LockPages       LockField = "pages"
	LockCover       LockField = "cover"
)

// LockSpec declares one lock field once. The three facts that used to be
// stated in five places — the wire name, the `books` column, and which
// flag on BookLocks it drives — travel together, so the projections
// (serialization, the toggle endpoint, the repo column list, the
// enrichment writability predicate) are derived from this rather than
// hand-kept alongside it.
type LockSpec struct {
	// Field is the name on the wire and the key clients send.
	Field LockField
	// Column is the `<field>_locked` column on the books table.
	Column string
	// Flag returns the BookLocks flag this spec owns. Returning a
	// pointer is what lets one declaration serve both the read
	// projections and the write ones.
	Flag func(*BookLocks) *bool
}

// LockSpecs is the lock vocabulary — the single list every lock
// projection walks.
//
// Adding a lock field is one entry here plus its migration; nothing else
// enumerates locks. Before this list existed the same field had to be
// added in five places and three of them failed silently: a missing
// serializer entry meant the flag never reached the client, a missing
// toggle-mapper case meant the endpoint accepted the key, validated it,
// and did nothing, and a missing writability check meant the field was
// never protected from a provider match. Only the repo column list
// failed loudly, and only on a count mismatch.
var LockSpecs = []LockSpec{
	{LockTitle, "title_locked", func(l *BookLocks) *bool { return &l.Title }},
	{LockSubtitle, "subtitle_locked", func(l *BookLocks) *bool { return &l.Subtitle }},
	{LockAuthor, "author_locked", func(l *BookLocks) *bool { return &l.Author }},
	{LockDescription, "description_locked", func(l *BookLocks) *bool { return &l.Description }},
	{LockPublisher, "publisher_locked", func(l *BookLocks) *bool { return &l.Publisher }},
	{LockSeries, "series_locked", func(l *BookLocks) *bool { return &l.Series }},
	{LockISBN, "isbn_locked", func(l *BookLocks) *bool { return &l.ISBN }},
	{LockISBN10, "isbn10_locked", func(l *BookLocks) *bool { return &l.ISBN10 }},
	{LockLanguage, "language_locked", func(l *BookLocks) *bool { return &l.Language }},
	{LockPublishDate, "publish_date_locked", func(l *BookLocks) *bool { return &l.PublishDate }},
	{LockGenres, "genres_locked", func(l *BookLocks) *bool { return &l.Genres }},
	{LockMoods, "moods_locked", func(l *BookLocks) *bool { return &l.Moods }},
	{LockTags, "tags_locked", func(l *BookLocks) *bool { return &l.Tags }},
	{LockPages, "pages_locked", func(l *BookLocks) *bool { return &l.Pages }},
	{LockCover, "cover_locked", func(l *BookLocks) *bool { return &l.Cover }},
}

// lockByField indexes LockSpecs for the wire-name lookups. Built once;
// a duplicate wire name or column panics at init rather than shadowing
// an earlier entry on a live request.
var lockByField = func() map[LockField]LockSpec {
	out := make(map[LockField]LockSpec, len(LockSpecs))
	cols := make(map[string]struct{}, len(LockSpecs))
	for _, s := range LockSpecs {
		if _, dup := out[s.Field]; dup {
			panic("model: duplicate lock field " + string(s.Field))
		}
		if _, dup := cols[s.Column]; dup {
			panic("model: duplicate lock column " + s.Column)
		}
		out[s.Field] = s
		cols[s.Column] = struct{}{}
	}
	return out
}()

// ParseLockField resolves a client-supplied key to a LockField. The
// toggle endpoint validates and applies through this one lookup, so a
// key that validates is by construction a key that applies — the shape
// that made "accepted, validated, ignored" possible is gone.
func ParseLockField(s string) (LockField, bool) {
	f := LockField(s)
	_, ok := lockByField[f]
	return f, ok
}

// Get reports whether f is locked. An unknown field reads as unlocked,
// which is the safe default: an unrecognised lock never shields a field
// from enrichment by accident.
func (l BookLocks) Get(f LockField) bool {
	s, ok := lockByField[f]
	if !ok {
		return false
	}
	return *s.Flag(&l)
}

// Set writes f, reporting whether f is a known lock field.
func (l *BookLocks) Set(f LockField, v bool) bool {
	s, ok := lockByField[f]
	if !ok {
		return false
	}
	*s.Flag(l) = v
	return true
}

// Locked returns the set fields in LockSpecs order. Callers that need a
// sparse projection (the book DTO) walk this.
func (l BookLocks) Locked() []LockField {
	var out []LockField
	for _, s := range LockSpecs {
		if *s.Flag(&l) {
			out = append(out, s.Field)
		}
	}
	return out
}

// LockColumns returns the `books` lock columns in LockSpecs order. The
// repo derives its SELECT fragment, scan destinations and UPDATE SET
// list from this, so the column order and the flag order are one
// traversal rather than two hand-kept lists (the Column-order coupling
// hazard in CONTEXT.md).
func LockColumns() []string {
	out := make([]string, len(LockSpecs))
	for i, s := range LockSpecs {
		out[i] = s.Column
	}
	return out
}

// LockFlags returns pointers to l's flags in LockSpecs order — the scan
// destinations for LockColumns, in the same order by construction.
func LockFlags(l *BookLocks) []any {
	out := make([]any, len(LockSpecs))
	for i, s := range LockSpecs {
		out[i] = s.Flag(l)
	}
	return out
}

// LockValues returns l's flags by value in LockSpecs order — the UPDATE
// arguments for LockColumns, from the same traversal that renders the
// SET list.
func LockValues(l BookLocks) []any {
	out := make([]any, len(LockSpecs))
	for i, s := range LockSpecs {
		out[i] = *s.Flag(&l)
	}
	return out
}
