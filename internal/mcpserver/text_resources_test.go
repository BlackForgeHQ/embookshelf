// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

func TestSearchBookText(t *testing.T) {
	h := newHarness()
	h.deps.Text = fakeText{search: service.TextSearchResult{
		Total: 7, TotalCapped: false, Partial: true,
		Matches: []service.TextMatch{{Offset: 1200, Snippet: "…the spice must flow…"}},
	}}
	cs := h.connect(t, reader)

	var out searchTextOut
	call(t, cs, "search_book_text", map[string]any{"book_id": bookID, "query": "spice"}, &out)
	if out.Status != statusOK || out.Total != 7 || !out.Partial || len(out.Matches) != 1 || out.Matches[0].Offset != 1200 {
		t.Fatalf("out = %+v", out)
	}

	if msg := errorText(t, call(t, cs, "search_book_text", map[string]any{"book_id": bookID, "query": " "}, nil)); msg != "query is required" {
		t.Fatalf("blank query error = %q", msg)
	}
}

func TestSearchBookTextPending(t *testing.T) {
	h := newHarness()
	h.deps.Text = fakeText{err: service.ErrRenditionPending}
	cs := h.connect(t, reader)

	var out searchTextOut
	res := call(t, cs, "search_book_text", map[string]any{"book_id": bookID, "query": "spice"}, &out)
	if res.IsError || out.Status != statusPending || out.Matches == nil {
		t.Fatalf("pending: isError=%v out=%+v", res.IsError, out)
	}
}

func TestGetBookTOC(t *testing.T) {
	h := newHarness()
	h.deps.Text = fakeText{outline: []service.BookOutlineEntry{
		{Title: "Book One", Level: 1, Offset: 0},
		{Title: "Chapter 1", Level: 2, Offset: 812},
	}}
	cs := h.connect(t, reader)

	var out tocOut
	call(t, cs, "get_book_toc", map[string]any{"book_id": bookID}, &out)
	if out.Status != statusOK || len(out.Entries) != 2 || out.Entries[1].Offset != 812 || out.Entries[1].Level != 2 || out.Truncated {
		t.Fatalf("out = %+v", out)
	}
}

func TestGetBookTOCCapsAndRefuses(t *testing.T) {
	h := newHarness()
	many := make([]service.BookOutlineEntry, maxOutlineEntries+10)
	for i := range many {
		many[i] = service.BookOutlineEntry{Title: fmt.Sprintf("H%d", i), Offset: int64(i)}
	}
	h.deps.Text = fakeText{outline: many}
	cs := h.connect(t, reader)

	var out tocOut
	call(t, cs, "get_book_toc", map[string]any{"book_id": bookID}, &out)
	if len(out.Entries) != maxOutlineEntries || !out.Truncated {
		t.Fatalf("got %d entries, truncated=%v", len(out.Entries), out.Truncated)
	}

	h = newHarness()
	h.deps.Text = fakeText{err: service.ErrNoBookText}
	cs = h.connect(t, reader)
	if msg := errorText(t, call(t, cs, "get_book_toc", map[string]any{"book_id": bookID}, nil)); msg != "no readable text for EPUB books" {
		t.Fatalf("textless error = %q", msg)
	}
}

// --- resources --------------------------------------------------------------

func readResource(t *testing.T, cs *mcp.ClientSession, uri string) (string, error) {
	t.Helper()
	res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", err
	}
	if len(res.Contents) != 1 || res.Contents[0].URI != uri || res.Contents[0].MIMEType != markdownMIME {
		t.Fatalf("contents = %+v", res.Contents)
	}
	return res.Contents[0].Text, nil
}

func TestBookResourceCarriesMetadataAndText(t *testing.T) {
	h := newHarness()
	h.books.books[bookID] = model.Book{
		ID: bookID, Title: "Dune", Author: "Frank Herbert", Format: "EPUB",
		Series: "Dune Chronicles", SeriesIndex: 1, Year: 1965, Description: "A desert planet.",
	}
	h.deps.Text = fakeText{text: "In the week before their departure to Arrakis…"}
	cs := h.connect(t, reader)

	doc, err := readResource(t, cs, bookURI(bookID))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Dune\n", "- Author: Frank Herbert", "- Series: Dune Chronicles #1", "- Year: 1965", "A desert planet.", "---", "In the week before"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "get_book_text") {
		t.Error("an uncut book points at get_book_text")
	}
}

func TestBookResourceSaysWhereToContinue(t *testing.T) {
	h := newHarness()
	h.deps.Text = fakeText{text: strings.Repeat("x", resourceTextCap+10)}
	cs := h.connect(t, reader)

	doc, err := readResource(t, cs, bookURI(bookID))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("get_book_text(book_id=%q, offset=%d)", bookID, resourceTextCap)
	if !strings.Contains(doc, want) {
		t.Fatalf("truncated document lacks %q", want)
	}
}

// A book whose text cannot be had is still a book worth attaching: the
// document keeps its metadata and says why the text is missing.
func TestBookResourceExplainsMissingText(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"pending":  {service.ErrRenditionPending, "being prepared"},
		"textless": {service.ErrNoBookText, "No text: no readable text for EPUB books"},
		"failed":   {&service.RenditionFailedError{Msg: "scanned PDF"}, "No text: could not read this book: scanned PDF"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness()
			h.deps.Text = fakeText{err: tc.err}
			cs := h.connect(t, reader)
			doc, err := readResource(t, cs, bookURI(bookID))
			if err != nil {
				t.Fatalf("read failed instead of degrading: %v", err)
			}
			if !strings.Contains(doc, "# Dune") || !strings.Contains(doc, tc.want) {
				t.Fatalf("document:\n%s\nwant %q", doc, tc.want)
			}
		})
	}
}

func TestBookResourceNotFound(t *testing.T) {
	cs := newHarness().connect(t, reader)
	for _, uri := range []string{bookURI(otherID), bookURI("not-a-uuid")} {
		if _, err := readResource(t, cs, uri); err == nil {
			t.Errorf("%s: read succeeded", uri)
		}
	}
}

func TestResourceListIsTheUsersLibraryPaged(t *testing.T) {
	h := newHarness()
	for i := range resourcePageSize + 5 {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		h.books.books[id] = model.Book{ID: id, Title: fmt.Sprintf("Book %03d", i), Format: "PDF"}
	}
	cs := h.connect(t, reader)

	var (
		seen   = map[string]bool{}
		cursor string
		pages  int
	)
	for {
		res, err := cs.ListResources(context.Background(), &mcp.ListResourcesParams{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, r := range res.Resources {
			if !strings.HasPrefix(r.URI, bookURIPrefix) || r.MIMEType != markdownMIME || r.Name == "" {
				t.Fatalf("resource = %+v", r)
			}
			seen[r.URI] = true
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if want := len(h.books.books); len(seen) != want || pages != 2 {
		t.Fatalf("listed %d books over %d pages, want %d over 2", len(seen), pages, want)
	}
	if h.books.params.Sort != "title" {
		t.Errorf("listed in %q order, want title", h.books.params.Sort)
	}
	for _, u := range h.books.seenUser {
		if u != reader.ID {
			t.Fatalf("listed as %q, want the token's user", u)
		}
	}
}

func TestResourceTemplateIsAdvertised(t *testing.T) {
	cs := newHarness().connect(t, reader)
	res, err := cs.ListResourceTemplates(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ResourceTemplates) != 1 || res.ResourceTemplates[0].URITemplate != bookURITemplate {
		t.Fatalf("templates = %+v", res.ResourceTemplates)
	}
}
