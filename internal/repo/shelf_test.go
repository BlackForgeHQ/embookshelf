package repo_test

import (
	"context"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// TestUnshelvedFilter exercises the SQL semantics of the "Unshelved"
// virtual view across both dialects:
//
//   - book on a regular shelf → excluded
//   - book on a smart shelf only → still counts as unshelved (smart-shelf
//     membership is query-time, not stored)
//   - book on the system `reading`/`finished` shelves only → still
//     counts as unshelved (those auto-populate from progress, not curation)
//   - book on no shelves → unshelved
//
// Covers both BookRepo.Search(...Unshelved=true) and
// ShelfRepo.CountUnshelvedForUser, since the count and the list must
// agree on membership.
func TestUnshelvedFilter(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		dialect := dialect
		t.Run(dialect, func(t *testing.T) {
			d := repotest.NewWithDialect(t, dialect)
			ur := repo.NewUserRepo(d)
			lr := repo.NewLibraryRepo(d)
			br := repo.NewBookRepo(d)
			sr := repo.NewShelfRepo(d)
			ctx := context.Background()

			alice, err := ur.Create(ctx, "alice@example.com", "Alice", "alice-hash", model.RoleUser)
			if err != nil {
				t.Fatalf("create alice: %v", err)
			}
			bob, err := ur.Create(ctx, "bob@example.com", "Bob", "bob-hash", model.RoleUser)
			if err != nil {
				t.Fatalf("create bob: %v", err)
			}

			lib, err := lr.CreateLibrary(ctx, "Lib", "lib", "/tmp/lib", nil)
			if err != nil {
				t.Fatalf("create library: %v", err)
			}

			mkBook := func(title string) model.Book {
				b, err := br.Create(ctx, model.Book{
					LibraryID: lib.ID,
					Title:     title,
					Format:    "EPUB",
				})
				if err != nil {
					t.Fatalf("create book %q: %v", title, err)
				}
				return b
			}

			onRegularShelf := mkBook("On Regular Shelf")
			onSmartShelfOnly := mkBook("On Smart Shelf Only")
			onReadingOnly := mkBook("On Reading Only")
			onFinishedOnly := mkBook("On Finished Only")
			completelyUnshelved := mkBook("Completely Unshelved")
			bobOnly := mkBook("Bob Shelved")

			// Alice's regular shelf with one book on it.
			favs, err := sr.Create(ctx, alice.ID, "Favourites", "accent", "library", nil)
			if err != nil {
				t.Fatalf("create favourites: %v", err)
			}
			if err := sr.AddBook(ctx, alice.ID, favs.Slug, onRegularShelf.ID); err != nil {
				t.Fatalf("add to favourites: %v", err)
			}

			// Alice's smart shelf — membership is rule-driven, never stored
			// in shelf_books. Books matching the rule must NOT be considered
			// shelved.
			rule := &model.ShelfRule{
				Match: model.RuleMatchAll,
				Predicates: []model.ShelfPredicate{{
					Field: model.RuleFieldTitle,
					Op:    model.OpEq,
					Value: onSmartShelfOnly.Title,
				}},
			}
			if _, err := sr.Create(ctx, alice.ID, "Smart", "accent", "sparkles", rule); err != nil {
				t.Fatalf("create smart shelf: %v", err)
			}

			// System shelves: insert directly with reserved slugs. These
			// auto-populate from progress and are excluded from the
			// "shelved" test.
			reading, err := sr.Create(ctx, alice.ID, "Reading", "accent", "book-open", nil)
			if err != nil {
				t.Fatalf("create reading: %v", err)
			}
			// The Create helper slugifies "Reading" to "reading", which is
			// exactly what we want.
			if reading.Slug != "reading" {
				t.Fatalf("reading slug = %q, want %q", reading.Slug, "reading")
			}
			if err := sr.AddBook(ctx, alice.ID, "reading", onReadingOnly.ID); err != nil {
				t.Fatalf("add to reading: %v", err)
			}
			finished, err := sr.Create(ctx, alice.ID, "Finished", "accent", "check-circle-2", nil)
			if err != nil {
				t.Fatalf("create finished: %v", err)
			}
			if finished.Slug != "finished" {
				t.Fatalf("finished slug = %q, want %q", finished.Slug, "finished")
			}
			if err := sr.AddBook(ctx, alice.ID, "finished", onFinishedOnly.ID); err != nil {
				t.Fatalf("add to finished: %v", err)
			}

			// Bob shelves bobOnly so we can verify per-user scoping: the
			// same book should still be unshelved for Alice.
			bobShelf, err := sr.Create(ctx, bob.ID, "Bob Shelf", "accent", "library", nil)
			if err != nil {
				t.Fatalf("create bob shelf: %v", err)
			}
			if err := sr.AddBook(ctx, bob.ID, bobShelf.Slug, bobOnly.ID); err != nil {
				t.Fatalf("add to bob shelf: %v", err)
			}

			gotBooks, err := br.Search(ctx, alice.ID, "", model.SearchParams{Unshelved: true})
			if err != nil {
				t.Fatalf("Search unshelved: %v", err)
			}

			gotIDs := map[string]bool{}
			for _, b := range gotBooks {
				gotIDs[b.ID] = true
			}

			wantUnshelved := []model.Book{
				onSmartShelfOnly,
				onReadingOnly,
				onFinishedOnly,
				completelyUnshelved,
				bobOnly,
			}
			for _, b := range wantUnshelved {
				if !gotIDs[b.ID] {
					t.Errorf("expected %q (id=%s) in unshelved list, got %d total", b.Title, b.ID, len(gotBooks))
				}
			}
			if gotIDs[onRegularShelf.ID] {
				t.Errorf("book on regular shelf must not appear in unshelved list")
			}

			gotCount, err := sr.CountUnshelvedForUser(ctx, alice.ID)
			if err != nil {
				t.Fatalf("CountUnshelvedForUser: %v", err)
			}
			if gotCount != len(wantUnshelved) {
				t.Errorf("CountUnshelvedForUser = %d, want %d", gotCount, len(wantUnshelved))
			}

			// Per-user scoping: Bob has shelved bobOnly, so Bob's
			// unshelved set must NOT include it.
			bobCount, err := sr.CountUnshelvedForUser(ctx, bob.ID)
			if err != nil {
				t.Fatalf("CountUnshelvedForUser(bob): %v", err)
			}
			// Bob has shelved exactly one of the six books, so the rest
			// (5) are unshelved for him.
			if bobCount != 5 {
				t.Errorf("Bob's unshelved count = %d, want 5", bobCount)
			}
		})
	}
}
