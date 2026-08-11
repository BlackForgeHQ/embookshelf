// SPDX-License-Identifier: AGPL-3.0-or-later

package repo_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/repo/repotest"
)

// The catalog seam's paging contract (#241): Search takes Limit/Offset,
// reports the total match count independent of the returned window, and
// Downloadable moves the "has a file on disk" filter into the same query
// so the total and the page agree.

// seedSearchLibrary creates a library and n books titled
// "<slug>-book-NNN". Every book gets a path except the ones listed in
// pathless (0-indexed).
func seedSearchLibrary(t *testing.T, br *repo.BookRepo, lr *repo.LibraryRepo, slug string, n int, pathless ...int) model.Library {
	t.Helper()
	ctx := context.Background()
	lib, err := lr.CreateLibrary(ctx, slug, slug, "/tmp/"+slug, nil)
	if err != nil {
		t.Fatalf("CreateLibrary(%s): %v", slug, err)
	}
	skip := make(map[int]bool, len(pathless))
	for _, i := range pathless {
		skip[i] = true
	}
	for i := 0; i < n; i++ {
		b := model.Book{
			LibraryID: lib.ID,
			Title:     fmt.Sprintf("%s-book-%03d", slug, i),
			Author:    "author",
			Format:    "EPUB",
		}
		if !skip[i] {
			b.Path = fmt.Sprintf("/tmp/%s/%03d.epub", slug, i)
		}
		if _, err := br.Create(ctx, b); err != nil {
			t.Fatalf("Create book %d: %v", i, err)
		}
	}
	return lib
}

func titles(books []model.Book) []string {
	out := make([]string, len(books))
	for i, b := range books {
		out[i] = b.Title
	}
	return out
}

func TestBookRepoSearch_PagingWindowAndTotal(t *testing.T) {
	d := repotest.New(t)
	br, lr := repo.NewBookRepo(d), repo.NewLibraryRepo(d)
	seedSearchLibrary(t, br, lr, "paging", 5)

	books, total, err := br.Search(context.Background(), "", "paging", model.SearchParams{
		Sort: "title", Limit: 2, Offset: 2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (the whole match count, not the window)", total)
	}
	got := titles(books)
	want := []string{"paging-book-002", "paging-book-003"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("page = %v, want %v", got, want)
	}
}

func TestBookRepoSearch_OffsetPastEndStillReportsTotal(t *testing.T) {
	d := repotest.New(t)
	br, lr := repo.NewBookRepo(d), repo.NewLibraryRepo(d)
	seedSearchLibrary(t, br, lr, "past", 3)

	books, total, err := br.Search(context.Background(), "", "past", model.SearchParams{
		Sort: "title", Limit: 2, Offset: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(books) != 0 {
		t.Errorf("books = %v, want empty page", titles(books))
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 — a previous-link needs the total even past the end", total)
	}
}

func TestBookRepoSearch_ZeroLimitKeepsUnpagedBehaviour(t *testing.T) {
	d := repotest.New(t)
	br, lr := repo.NewBookRepo(d), repo.NewLibraryRepo(d)
	seedSearchLibrary(t, br, lr, "unpaged", 4)

	books, total, err := br.Search(context.Background(), "", "unpaged", model.SearchParams{Sort: "title"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(books) != 4 || total != 4 {
		t.Errorf("got %d books, total %d, want 4/4 — Limit 0 must stay the JSON API's unpaged read", len(books), total)
	}
}

func TestBookRepoSearch_DownloadableExcludesPathlessFromPageAndTotal(t *testing.T) {
	d := repotest.New(t)
	br, lr := repo.NewBookRepo(d), repo.NewLibraryRepo(d)
	seedSearchLibrary(t, br, lr, "dl", 4, 1) // book 001 has no file

	books, total, err := br.Search(context.Background(), "", "dl", model.SearchParams{
		Sort: "title", Downloadable: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 — pathless books must not count toward paging", total)
	}
	for _, b := range books {
		if b.Path == "" {
			t.Errorf("book %s has no path but survived Downloadable", b.Title)
		}
	}
}

func TestBookRepoSearch_EmptySlugPagesAcrossLibrariesGlobally(t *testing.T) {
	d := repotest.New(t)
	br, lr := repo.NewBookRepo(d), repo.NewLibraryRepo(d)
	// Titles interleave across the two libraries under a global title
	// sort: alpha-book-000, beta-book-000 sort after alpha but the page
	// window must respect the merged order, not per-library blocks.
	seedSearchLibrary(t, br, lr, "alpha", 2)
	seedSearchLibrary(t, br, lr, "beta", 2)

	books, total, err := br.Search(context.Background(), "", "", model.SearchParams{
		Sort: "title", Limit: 3, Offset: 0,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 across both libraries", total)
	}
	got := titles(books)
	want := []string{"alpha-book-000", "alpha-book-001", "beta-book-000"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("page = %v, want %v — one globally ordered list, not per-library blocks", got, want)
	}
}
