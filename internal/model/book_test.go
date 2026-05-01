package model_test

import (
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
)

func TestEditableMetadata_IsZero(t *testing.T) {
	if !(model.EditableMetadata{}).IsZero() {
		t.Error("zero EditableMetadata: IsZero=false, want true")
	}
	if (model.EditableMetadata{Title: "x"}).IsZero() {
		t.Error("non-zero Title: IsZero=true, want false")
	}
	if (model.EditableMetadata{Tags: []string{"a"}}).IsZero() {
		t.Error("non-zero Tags: IsZero=true, want false")
	}
}

func TestEditableMetadata_Merge_OverlayWins(t *testing.T) {
	base := model.EditableMetadata{Title: "Base", Author: "Base"}
	overlay := model.EditableMetadata{Title: "Overlay"}
	got := model.MergeEditable(base, overlay)
	if got.Title != "Overlay" {
		t.Errorf("Title=%q want Overlay", got.Title)
	}
	if got.Author != "Base" {
		t.Errorf("Author=%q want Base (overlay zero)", got.Author)
	}
}

func TestEditableMetadata_Merge_TagsOverwriteWhenNonEmpty(t *testing.T) {
	base := model.EditableMetadata{Tags: []string{"a", "b"}}
	overlay := model.EditableMetadata{Tags: []string{"x"}}
	got := model.MergeEditable(base, overlay)
	if len(got.Tags) != 1 || got.Tags[0] != "x" {
		t.Errorf("Tags=%v want [x]", got.Tags)
	}
}

func TestBook_Editable_RoundTrip(t *testing.T) {
	b := model.Book{
		ID:          "b1",
		Title:       "T",
		Author:      "A",
		Description: "D",
		Tags:        []string{"x", "y"},
	}
	em := b.Editable()
	if em.Title != "T" || em.Author != "A" {
		t.Errorf("Editable() lost scalars: %+v", em)
	}
	var b2 model.Book
	b2.ApplyEditable(em)
	if b2.Title != "T" || b2.Author != "A" {
		t.Errorf("ApplyEditable lost scalars: %+v", b2)
	}
	if b2.ID != "" {
		t.Errorf("ApplyEditable touched ID: %q", b2.ID)
	}
}
