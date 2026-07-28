// SPDX-License-Identifier: AGPL-3.0-or-later

package model

// editableField declares one member of the editable metadata set once.
// The four walks that used to state the set separately — IsZero,
// MergeEditable, Book.Editable and Book.ApplyEditable — are derived from
// the list below, so a field added to EditableMetadata cannot reach the
// sidecar while staying invisible to the merge, or survive a round trip
// through Book by accident.
type editableField struct {
	// Name is the JSON name from EditableMetadata's tag. Used by the
	// parity test to tie this list to the struct.
	Name string
	// Empty reports whether em carries nothing for this field. "Nothing"
	// is the zero value: the empty string, zero, or an empty slice.
	Empty func(em EditableMetadata) bool
	// Copy overwrites dst's field with src's.
	Copy func(dst *EditableMetadata, src EditableMetadata)
	// FromBook and ToBook project the field onto and off a Book. Both
	// nil for a field Book stores in a different shape — see
	// PublishedDate below — and the parity test insists they are nil
	// together.
	FromBook func(em *EditableMetadata, b Book)
	ToBook   func(b *Book, em EditableMetadata)
}

// editableStr declares a string-valued editable field. bookPtr is nil
// when Book does not store the field as a plain string.
func editableStr(name string, emPtr func(*EditableMetadata) *string, bookPtr func(*Book) *string) editableField {
	f := editableField{
		Name:  name,
		Empty: func(em EditableMetadata) bool { return *emPtr(&em) == "" },
		Copy:  func(dst *EditableMetadata, src EditableMetadata) { *emPtr(dst) = *emPtr(&src) },
	}
	if bookPtr != nil {
		f.FromBook = func(em *EditableMetadata, b Book) { *emPtr(em) = *bookPtr(&b) }
		f.ToBook = func(b *Book, em EditableMetadata) { *bookPtr(b) = *emPtr(&em) }
	}
	return f
}

// editableInt declares an int-valued editable field.
func editableInt(name string, emPtr func(*EditableMetadata) *int, bookPtr func(*Book) *int) editableField {
	f := editableField{
		Name:  name,
		Empty: func(em EditableMetadata) bool { return *emPtr(&em) == 0 },
		Copy:  func(dst *EditableMetadata, src EditableMetadata) { *emPtr(dst) = *emPtr(&src) },
	}
	if bookPtr != nil {
		f.FromBook = func(em *EditableMetadata, b Book) { *emPtr(em) = *bookPtr(&b) }
		f.ToBook = func(b *Book, em EditableMetadata) { *bookPtr(b) = *emPtr(&em) }
	}
	return f
}

// editableSlice declares a []string-valued editable field.
func editableSlice(name string, emPtr func(*EditableMetadata) *[]string, bookPtr func(*Book) *[]string) editableField {
	f := editableField{
		Name:  name,
		Empty: func(em EditableMetadata) bool { return len(*emPtr(&em)) == 0 },
		Copy:  func(dst *EditableMetadata, src EditableMetadata) { *emPtr(dst) = *emPtr(&src) },
	}
	if bookPtr != nil {
		f.FromBook = func(em *EditableMetadata, b Book) { *emPtr(em) = *bookPtr(&b) }
		f.ToBook = func(b *Book, em EditableMetadata) { *bookPtr(b) = *emPtr(&em) }
	}
	return f
}

// editableFields is the editable metadata set — declared once, in
// EditableMetadata's field order.
//
// Adding an editable field is one entry here plus the struct field;
// IsZero, MergeEditable, Editable and ApplyEditable all follow.
var editableFields = []editableField{
	editableStr("title",
		func(e *EditableMetadata) *string { return &e.Title },
		func(b *Book) *string { return &b.Title }),
	editableStr("subtitle",
		func(e *EditableMetadata) *string { return &e.Subtitle },
		func(b *Book) *string { return &b.Subtitle }),
	editableStr("author",
		func(e *EditableMetadata) *string { return &e.Author },
		func(b *Book) *string { return &b.Author }),
	editableStr("description",
		func(e *EditableMetadata) *string { return &e.Description },
		func(b *Book) *string { return &b.Description }),
	editableStr("language",
		func(e *EditableMetadata) *string { return &e.Language },
		func(b *Book) *string { return &b.Language }),
	editableStr("publisher",
		func(e *EditableMetadata) *string { return &e.Publisher },
		func(b *Book) *string { return &b.Publisher }),
	// PublishedDate has no Book projection: Book.PublishDate is a
	// *time.Time, and the string ↔ time conversion belongs at the
	// boundary that knows the layout (BookPatch.applyPublishDate), not
	// in a field copy. Editable() therefore leaves it blank and
	// ApplyEditable leaves Book.PublishDate alone — long-standing
	// behaviour, now stated once instead of implied by two omissions.
	editableStr("published_date",
		func(e *EditableMetadata) *string { return &e.PublishedDate }, nil),
	editableStr("isbn",
		func(e *EditableMetadata) *string { return &e.ISBN },
		func(b *Book) *string { return &b.ISBN }),
	editableStr("series",
		func(e *EditableMetadata) *string { return &e.Series },
		func(b *Book) *string { return &b.Series }),
	editableInt("series_index",
		func(e *EditableMetadata) *int { return &e.SeriesIndex },
		func(b *Book) *int { return &b.SeriesIndex }),
	editableSlice("tags",
		func(e *EditableMetadata) *[]string { return &e.Tags },
		func(b *Book) *[]string { return &b.Tags }),
	editableSlice("genres",
		func(e *EditableMetadata) *[]string { return &e.Genres },
		func(b *Book) *[]string { return &b.Genres }),
}
