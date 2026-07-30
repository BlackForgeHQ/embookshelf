// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/db"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// The annotations table used to keep three positionally ordered lists in
// step by hand — the aliased SELECT list, the RETURNING list, and the
// Scan destinations. `selected_text`, `note` and `color` are three
// adjacent TEXT columns, so swapping any two of them compiled, ran, and
// crossed every annotation's text with its colour on every row. This is
// the Column-order coupling hazard from CONTEXT.md.
//
// The defence is that every field below carries a value distinct from
// every other field of its type, so any crossing surfaces as a mismatch.
// The three uuid columns are distinct by construction; the four TEXT
// columns are given deliberately unmistakable values. `created_at` and
// `updated_at` are equal on a fresh row, so the two timestamps can only
// be told apart after an update — which is what the update test pins.

// annotationFixture creates the user and book an annotation hangs off.
func annotationFixture(t *testing.T, d *db.DB) (userID, bookID string) {
	t.Helper()
	ctx := context.Background()

	user, err := repo.NewUserRepo(d).Create(ctx, "annotator@example.com", "Annotator", "hash", model.RoleUser)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	lib, err := repo.NewLibraryRepo(d).CreateLibrary(ctx, "Annotations", "annotations", "/tmp/annotations", nil)
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	book, err := repo.NewBookRepo(d).Create(ctx, model.Book{
		LibraryID: lib.ID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB", Path: "dune.epub",
	})
	if err != nil {
		t.Fatalf("create book: %v", err)
	}
	return user.ID, book.ID
}

// distinctAnnotation returns an annotation whose every string field
// differs from every other string field. tag varies the values so the
// same helper can produce a second, wholly different generation for the
// update path.
func distinctAnnotation(userID, bookID, tag string) model.Annotation {
	return model.Annotation{
		UserID:       userID,
		BookID:       bookID,
		Locator:      tag + "-locator-epubcfi(/6/14!/4/2)",
		SelectedText: tag + "-selected-text",
		Note:         tag + "-note",
		Color:        tag + "-color",
	}
}

// assertAnnotationFields compares field by field rather than with a
// single struct equality, so a failure names the columns that crossed.
func assertAnnotationFields(t *testing.T, where string, got, want model.Annotation) {
	t.Helper()
	for _, c := range []struct {
		field     string
		got, want string
	}{
		{"UserID", got.UserID, want.UserID},
		{"BookID", got.BookID, want.BookID},
		{"Locator", got.Locator, want.Locator},
		{"SelectedText", got.SelectedText, want.SelectedText},
		{"Note", got.Note, want.Note},
		{"Color", got.Color, want.Color},
	} {
		if c.got != c.want {
			t.Errorf("%s: %s = %q, want %q — a column/scan-order crossing looks exactly like this",
				where, c.field, c.got, c.want)
		}
	}
	if want.ID != "" && got.ID != want.ID {
		t.Errorf("%s: ID = %q, want %q", where, got.ID, want.ID)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("%s: CreatedAt is zero — the timestamp columns did not land", where)
	}
	if got.UpdatedAt.IsZero() {
		t.Errorf("%s: UpdatedAt is zero — the timestamp columns did not land", where)
	}
}

// Create → Get → ListForBook → ListRecent exercises every read path:
// the RETURNING list on the write, and the aliased SELECT list on all
// three reads.
func TestAnnotationRepo_RoundTripPreservesEveryField(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	userID, bookID := annotationFixture(t, d)
	ar := repo.NewAnnotationRepo(d)

	want := distinctAnnotation(userID, bookID, "created")
	created, err := ar.Create(ctx, want)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertAnnotationFields(t, "Create", created, want)
	if created.ID == "" {
		t.Fatal("Create returned an empty id")
	}
	// The id must be its own column, not a copy of either foreign key.
	if created.ID == userID || created.ID == bookID {
		t.Errorf("Create: ID = %q, which is the user or book id — the uuid columns crossed", created.ID)
	}

	want.ID = created.ID

	got, err := ar.Get(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	assertAnnotationFields(t, "Get", got, want)

	forBook, err := ar.ListForBook(ctx, userID, bookID)
	if err != nil {
		t.Fatalf("ListForBook: %v", err)
	}
	if len(forBook) != 1 {
		t.Fatalf("ListForBook returned %d rows, want 1", len(forBook))
	}
	assertAnnotationFields(t, "ListForBook", forBook[0], want)

	recent, err := ar.ListRecent(ctx, userID, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("ListRecent returned %d rows, want 1", len(recent))
	}
	assertAnnotationFields(t, "ListRecent", recent[0], want)
}

// Update patches the three user-editable strings and returns the row
// through the same RETURNING list. A second, wholly different generation
// of values proves the SET list still names the column each argument was
// meant for, and that the returned row is read back in the right order.
func TestAnnotationRepo_UpdateWritesEveryFieldToItsOwnColumn(t *testing.T) {
	d := repotest.New(t)
	ctx := context.Background()
	userID, bookID := annotationFixture(t, d)
	ar := repo.NewAnnotationRepo(d)

	created, err := ar.Create(ctx, distinctAnnotation(userID, bookID, "first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := distinctAnnotation(userID, bookID, "second")
	want.ID = created.ID
	// Update does not touch the locator, so it keeps the first value.
	want.Locator = created.Locator

	updated, err := ar.Update(ctx, userID, created.ID, &want.Note, &want.SelectedText, &want.Color)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertAnnotationFields(t, "Update", updated, want)

	got, err := ar.Get(ctx, userID, created.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	assertAnnotationFields(t, "Get after update", got, want)

	// created_at and updated_at are the only same-typed pair left, and
	// they are equal on a fresh row. The update moves updated_at
	// forward, which is the only moment a crossing of the two is
	// visible.
	if !got.UpdatedAt.After(got.CreatedAt) {
		t.Errorf("UpdatedAt (%v) is not after CreatedAt (%v) — the timestamp columns crossed",
			got.UpdatedAt, got.CreatedAt)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v — the update rewrote the creation time",
			got.CreatedAt, created.CreatedAt)
	}
}
