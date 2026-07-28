// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"reflect"
	"strings"
	"testing"
)

// The parity test for the editable metadata set. editableFields is the
// declaration; IsZero, MergeEditable, Editable and ApplyEditable are its
// projections. These assertions are what makes "declared once" true
// rather than aspirational.

// editableWithoutBookProjection names the fields Book stores in a
// different shape, so the round-trip below knows not to expect them
// back. Exactly one today.
var editableWithoutBookProjection = map[string]string{
	"published_date": "Book.PublishDate is *time.Time; the layout conversion lives at the boundary",
}

// TestEditableFieldsCoverStruct ties the declaration to the struct: one
// entry per EditableMetadata field, in order, named by its JSON tag.
func TestEditableFieldsCoverStruct(t *testing.T) {
	typ := reflect.TypeOf(EditableMetadata{})
	if typ.NumField() != len(editableFields) {
		t.Fatalf("EditableMetadata has %d fields but editableFields declares %d: a field with no entry is invisible to IsZero, MergeEditable, Editable and ApplyEditable",
			typ.NumField(), len(editableFields))
	}
	for i := 0; i < typ.NumField(); i++ {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if got := editableFields[i].Name; got != tag {
			t.Errorf("editableFields[%d].Name = %q, want %q (EditableMetadata.%s json tag)",
				i, got, tag, typ.Field(i).Name)
		}
	}
}

// TestEditableFieldsProjectionsPaired asserts FromBook and ToBook are nil
// together — a field that can be read off a Book must be writable back,
// or Editable/ApplyEditable stop being inverses.
func TestEditableFieldsProjectionsPaired(t *testing.T) {
	for _, f := range editableFields {
		if (f.FromBook == nil) != (f.ToBook == nil) {
			t.Errorf("editable field %q has FromBook=%v ToBook=%v; both or neither",
				f.Name, f.FromBook != nil, f.ToBook != nil)
		}
		_, declared := editableWithoutBookProjection[f.Name]
		if f.FromBook == nil && !declared {
			t.Errorf("editable field %q has no Book projection but is not declared in editableWithoutBookProjection with a reason", f.Name)
		}
		if f.FromBook != nil && declared {
			t.Errorf("editable field %q has a Book projection but is declared as lacking one", f.Name)
		}
	}
}

// TestEditableIsZeroSeesEveryField is the zero-check projection: setting
// any single field must make IsZero false. A field missing from the
// declaration would leave IsZero true and silently skip the merge.
func TestEditableIsZeroSeesEveryField(t *testing.T) {
	if !(EditableMetadata{}).IsZero() {
		t.Fatal("zero EditableMetadata is not IsZero")
	}
	for _, f := range editableFields {
		em := nonZeroEditable(t, f.Name)
		if em.IsZero() {
			t.Errorf("IsZero() = true with only %q set", f.Name)
		}
	}
}

// TestEditableMergeSeesEveryField is the merge projection: every field
// set on the overlay must win over an empty base, and an empty overlay
// must never clobber the base.
func TestEditableMergeSeesEveryField(t *testing.T) {
	for _, f := range editableFields {
		overlay := nonZeroEditable(t, f.Name)

		got := MergeEditable(EditableMetadata{}, overlay)
		if f.Empty(got) {
			t.Errorf("MergeEditable(zero, %q) did not carry the field through", f.Name)
		}

		// An empty overlay must leave a populated base alone.
		kept := MergeEditable(overlay, EditableMetadata{})
		if f.Empty(kept) {
			t.Errorf("MergeEditable(%q, zero) dropped the field", f.Name)
		}
	}
}

// TestEditableRoundTripsThroughBook is the apply projection: every field
// with a Book projection must survive Book -> Editable -> Book.
func TestEditableRoundTripsThroughBook(t *testing.T) {
	for _, f := range editableFields {
		if f.FromBook == nil {
			continue
		}
		em := nonZeroEditable(t, f.Name)

		var b Book
		b.ApplyEditable(em)
		back := b.Editable()

		if f.Empty(back) {
			t.Errorf("field %q did not survive ApplyEditable -> Editable: ToBook and FromBook disagree", f.Name)
		}
	}
}

// TestEditableApplyIgnoresUnprojected pins the surprising rule: a
// published date on the payload must NOT reach Book, because Book stores
// it as a parsed *time.Time. This was previously implied by the field's
// absence from two hand-written walkers.
func TestEditableApplyIgnoresUnprojected(t *testing.T) {
	var b Book
	b.ApplyEditable(EditableMetadata{PublishedDate: "2001-02-03"})
	if b.PublishDate != nil {
		t.Errorf("ApplyEditable set Book.PublishDate = %v, want nil (conversion belongs at the boundary)", b.PublishDate)
	}
	if got := (Book{}).Editable().PublishedDate; got != "" {
		t.Errorf("Editable().PublishedDate = %q, want empty", got)
	}
}

// nonZeroEditable returns an EditableMetadata with exactly the named
// field set to a non-zero value, driven by the declaration itself.
func nonZeroEditable(t *testing.T, name string) EditableMetadata {
	t.Helper()
	var em EditableMetadata
	v := reflect.ValueOf(&em).Elem()
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		if strings.Split(typ.Field(i).Tag.Get("json"), ",")[0] != name {
			continue
		}
		switch fv := v.Field(i); fv.Kind() {
		case reflect.String:
			fv.SetString("x-" + name)
		case reflect.Int:
			fv.SetInt(7)
		case reflect.Slice:
			fv.Set(reflect.ValueOf([]string{"x-" + name}))
		default:
			t.Fatalf("nonZeroEditable: unhandled kind %s for %q", fv.Kind(), name)
		}
		return em
	}
	t.Fatalf("nonZeroEditable: no EditableMetadata field tagged %q", name)
	return em
}

// editableToPatchField maps each editable field onto the BookPatch field
// that carries it. BookPatch is the second editable surface — the PATCH
// endpoint's — and it is wider than EditableMetadata (it also carries
// Format, Year, Rating, Palette, ISBN10, SeriesTotal, AgeRating,
// ContentRating, Pages and PublicReviews, none of which the sidecar
// holds). What must not drift is the overlap: a field the sidecar can
// carry that no patch can set is a field the edit UI cannot reach.
var editableToPatchField = map[string]string{
	"title":          "Title",
	"subtitle":       "Subtitle",
	"author":         "Author",
	"description":    "Description",
	"language":       "Language",
	"publisher":      "Publisher",
	"published_date": "PublishDate",
	"isbn":           "ISBN",
	"series":         "Series",
	"series_index":   "SeriesNum",
	"tags":           "Tags",
	"genres":         "Genres",
}

// TestEditableSetReachableViaBookPatch keeps the two editable surfaces in
// step: every field of the sidecar/in-file editable set must be settable
// through a BookPatch, and the mapping must stay exhaustive.
func TestEditableSetReachableViaBookPatch(t *testing.T) {
	if len(editableToPatchField) != len(editableFields) {
		t.Fatalf("editableToPatchField has %d entries, editableFields has %d: map the new field onto its BookPatch field",
			len(editableToPatchField), len(editableFields))
	}
	patch := reflect.TypeOf(BookPatch{})
	for _, f := range editableFields {
		name, ok := editableToPatchField[f.Name]
		if !ok {
			t.Errorf("editable field %q has no BookPatch field mapped", f.Name)
			continue
		}
		pf, ok := patch.FieldByName(name)
		if !ok {
			t.Errorf("editable field %q maps to BookPatch.%s, which does not exist", f.Name, name)
			continue
		}
		if pf.Type.Kind() != reflect.Pointer {
			t.Errorf("BookPatch.%s is %s, want a pointer (nil means \"leave alone\")", name, pf.Type)
		}
	}
}
