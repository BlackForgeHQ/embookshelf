// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/fileproc"
	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/service"
)

const (
	defaultPageSize = 20_000
	maxPageSize     = 50_000

	defaultSearchResults = 20
	maxSearchResults     = 100

	// maxOutlineEntries bounds a table of contents. A converted PDF can
	// turn every bold line into a heading; past this the list stops
	// being a map.
	maxOutlineEntries = 500
)

const (
	statusOK      = "ok"
	statusPending = "pending"
	pendingMsg    = "This book's text is being prepared. Try again in a minute."
)

// textRefusal maps a BookText failure to what the model is told. Shared
// by every text tool and the book resource, so a book that cannot be read
// is explained the same way wherever it is asked about.
//
// pending is not a failure: the text is one conversion away and has been
// asked for (ADR-0039 §5), so callers answer it as a result. Anything not
// recognised comes back as-is and withUser logs it as internal.
func textRefusal(b model.Book, err error) (pending bool, _ error) {
	var failed *service.RenditionFailedError
	switch {
	case errors.Is(err, service.ErrRenditionPending):
		return true, nil
	case errors.As(err, &failed):
		return false, failf("could not read this book: " + failed.Msg)
	case errors.Is(err, service.ErrNoBookText):
		return false, failf(fmt.Sprintf("no readable text for %s books", b.Format))
	case errors.Is(err, fileproc.ErrNoReadableText):
		return false, failf("this book has no readable text (it may be images only)")
	}
	return false, err
}

// --- get_book_text ----------------------------------------------------------

type bookTextIn struct {
	BookID   string `json:"book_id" jsonschema:"from search_library or list_shelf_books"`
	Offset   int64  `json:"offset,omitempty" jsonschema:"where to start: 0, next_offset from the previous call, or an offset from get_book_toc or search_book_text"`
	MaxChars int64  `json:"max_chars,omitempty" jsonschema:"page size, default 20000, at most 50000"`
}

type bookTextOut struct {
	Status string `json:"status" jsonschema:"ok, or pending while the text is being prepared (retry in a minute)"`
	// Message explains a pending status.
	Message    string `json:"message,omitempty"`
	Text       string `json:"text"`
	Offset     int64  `json:"offset" jsonschema:"where this page starts"`
	NextOffset *int64 `json:"next_offset" jsonschema:"pass as offset for the next page; null at the end of the book"`
}

func (t tools) getBookText(ctx context.Context, u model.User, in bookTextIn) (bookTextOut, error) {
	b, err := t.lookupBook(ctx, u, in.BookID)
	if err != nil {
		return bookTextOut{}, err
	}
	if in.Offset < 0 {
		return bookTextOut{}, failf("offset must not be negative")
	}
	size := in.MaxChars
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)

	page, err := t.d.Text.Page(ctx, b, in.Offset, size)
	if err != nil {
		pending, rerr := textRefusal(b, err)
		if !pending {
			return bookTextOut{}, rerr
		}
		return bookTextOut{Status: statusPending, Message: pendingMsg, Offset: in.Offset}, nil
	}
	return bookTextOut{Status: statusOK, Text: page.Text, Offset: page.Offset, NextOffset: page.NextOffset}, nil
}

// --- search_book_text -------------------------------------------------------

type searchTextIn struct {
	BookID     string `json:"book_id" jsonschema:"from search_library or list_shelf_books"`
	Query      string `json:"query" jsonschema:"words to find, in this order; case-insensitive; matches inside longer words too"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"at most this many matches, default 20, at most 100"`
}

type textMatchOut struct {
	Offset  int64  `json:"offset" jsonschema:"where the match starts; pass to get_book_text (a little lower for context before it)"`
	Snippet string `json:"snippet"`
}

type searchTextOut struct {
	Status      string         `json:"status" jsonschema:"ok, or pending while the text is being prepared (retry in a minute)"`
	Message     string         `json:"message,omitempty"`
	Total       int            `json:"total" jsonschema:"matches in the whole book, not just those returned"`
	TotalCapped bool           `json:"total_capped,omitempty" jsonschema:"counting stopped early; there are at least total matches"`
	Partial     bool           `json:"partial,omitempty" jsonschema:"the book is very long and only its beginning was searched"`
	Matches     []textMatchOut `json:"matches"`
}

func (t tools) searchBookText(ctx context.Context, u model.User, in searchTextIn) (searchTextOut, error) {
	b, err := t.lookupBook(ctx, u, in.BookID)
	if err != nil {
		return searchTextOut{}, err
	}
	n := in.MaxResults
	if n <= 0 {
		n = defaultSearchResults
	}
	n = min(n, maxSearchResults)

	res, err := t.d.Text.Search(ctx, b, in.Query, n)
	if errors.Is(err, service.ErrEmptyTextQuery) {
		return searchTextOut{}, failf("query is required")
	}
	if err != nil {
		pending, rerr := textRefusal(b, err)
		if !pending {
			return searchTextOut{}, rerr
		}
		return searchTextOut{Status: statusPending, Message: pendingMsg, Matches: []textMatchOut{}}, nil
	}
	out := searchTextOut{
		Status: statusOK, Total: res.Total, TotalCapped: res.TotalCapped, Partial: res.Partial,
		Matches: make([]textMatchOut, 0, len(res.Matches)),
	}
	for _, m := range res.Matches {
		out.Matches = append(out.Matches, textMatchOut{Offset: m.Offset, Snippet: m.Snippet})
	}
	return out, nil
}

// --- get_book_toc -----------------------------------------------------------

type tocEntryOut struct {
	Title  string `json:"title"`
	Level  int    `json:"level,omitempty" jsonschema:"heading depth, 1 for top level; absent when the book's contents are flat"`
	Offset int64  `json:"offset" jsonschema:"where this section starts; pass to get_book_text"`
}

type tocOut struct {
	Status    string        `json:"status" jsonschema:"ok, or pending while the text is being prepared (retry in a minute)"`
	Message   string        `json:"message,omitempty"`
	Entries   []tocEntryOut `json:"entries"`
	Truncated bool          `json:"truncated,omitempty" jsonschema:"the contents were cut short; read on with get_book_text past the last entry"`
}

func (t tools) getBookTOC(ctx context.Context, u model.User, in bookIDIn) (tocOut, error) {
	b, err := t.lookupBook(ctx, u, in.BookID)
	if err != nil {
		return tocOut{}, err
	}
	entries, err := t.d.Text.Outline(ctx, b)
	if err != nil {
		pending, rerr := textRefusal(b, err)
		if !pending {
			return tocOut{}, rerr
		}
		return tocOut{Status: statusPending, Message: pendingMsg, Entries: []tocEntryOut{}}, nil
	}
	out := tocOut{Status: statusOK, Entries: make([]tocEntryOut, 0, min(len(entries), maxOutlineEntries))}
	if len(entries) > maxOutlineEntries {
		entries, out.Truncated = entries[:maxOutlineEntries], true
	}
	for _, e := range entries {
		out.Entries = append(out.Entries, tocEntryOut{Title: e.Title, Level: e.Level, Offset: e.Offset})
	}
	return out, nil
}

func (t tools) registerText(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_book_text",
		Description: "Read a book's text, one page at a time. EPUB and PDF books have text; comics and audiobooks do not. " +
			"Start at offset 0, or at an offset from get_book_toc or search_book_text, and pass next_offset back to continue; next_offset is null at the end.",
		Annotations: readOnly("Read book text"),
	}, withUser("get_book_text", t.getBookText))

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_book_text",
		Description: "Find where a book mentions something. Returns each match's offset and a snippet around it; " +
			"pass an offset to get_book_text to read the passage.",
		Annotations: readOnly("Search in book"),
	}, withUser("search_book_text", t.searchBookText))

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_book_toc",
		Description: "Get a book's table of contents: chapter titles with the offset where each starts. " +
			"Pass an offset to get_book_text to read that chapter.",
		Annotations: readOnly("Table of contents"),
	}, withUser("get_book_toc", t.getBookTOC))
}
