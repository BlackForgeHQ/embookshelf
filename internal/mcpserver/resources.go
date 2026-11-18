// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

// Books as MCP resources: embookshelf://book/{id} is a book's metadata
// and text as one Markdown document, for a client to attach as context.
// resources/list is the user's library, so a client can offer it in a
// picker.

const (
	bookURIPrefix   = "embookshelf://book/"
	bookURITemplate = bookURIPrefix + "{book_id}"
	markdownMIME    = "text/markdown"

	// resourceTextCap bounds the text a resource carries: about 64k
	// tokens, enough for most of a novel without crowding out the
	// conversation it was attached to. Past it, the document says where
	// to continue with get_book_text.
	resourceTextCap = 256 << 10

	// resourcePageSize is how many books one resources/list page holds.
	resourcePageSize = 100
)

func bookURI(id string) string { return bookURIPrefix + id }

func (t tools) registerResources(s *mcp.Server) {
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "book",
		Title:       "Book",
		Description: "A book's metadata and text as one Markdown document. Long books are cut short; continue with get_book_text.",
		URITemplate: bookURITemplate,
		MIMEType:    markdownMIME,
	}, t.readBook)

	// The SDK's resources/list answers from resources registered up
	// front, the same for every caller. A library is neither fixed nor
	// shared — each user lists their own — so the method is answered
	// here, per request, before the SDK's handler would run.
	s.AddReceivingMiddleware(t.listBooks)
}

// listBooks answers resources/list with the caller's books, by title, a
// page at a time. Every other method passes through.
func (t tools) listBooks(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "resources/list" {
			return next(ctx, method, req)
		}
		u, ok := userOf(req.GetExtra())
		if !ok {
			slog.Error("mcp resources/list without a user")
			return nil, errInternal
		}
		offset := 0
		if p, ok := req.GetParams().(*mcp.ListResourcesParams); ok && p != nil && p.Cursor != "" {
			n, err := strconv.Atoi(p.Cursor)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid cursor %q", p.Cursor)
			}
			offset = n
		}
		books, total, err := t.d.Books.Search(ctx, u.ID, "", model.SearchParams{
			Sort: "title", Limit: resourcePageSize, Offset: offset,
		})
		if err != nil {
			slog.Error("mcp resources/list", "user", u.ID, "err", err)
			return nil, errInternal
		}
		res := &mcp.ListResourcesResult{Resources: make([]*mcp.Resource, 0, len(books))}
		for _, b := range books {
			res.Resources = append(res.Resources, &mcp.Resource{
				URI:         bookURI(b.ID),
				Name:        b.Title,
				Title:       displayTitle(b),
				Description: byline(b),
				MIMEType:    markdownMIME,
			})
		}
		if next := offset + len(books); len(books) > 0 && next < total {
			res.NextCursor = strconv.Itoa(next)
		}
		return res, nil
	}
}

// readBook answers resources/read for embookshelf://book/{id}.
//
// The document always carries the metadata. Its text section degrades in
// words rather than failing the read: a comic, a PDF still converting or
// one whose conversion failed is still a book worth attaching, and the
// note says why its text is missing. Only an unknown book is an error.
func (t tools) readBook(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	u, ok := userOf(req.Extra)
	if !ok {
		slog.Error("mcp resource read without a user")
		return nil, errInternal
	}
	uri := req.Params.URI
	id, found := strings.CutPrefix(uri, bookURIPrefix)
	if !found {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	b, err := t.d.Books.GetByID(ctx, u.ID, id)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, mcp.ResourceNotFoundError(uri)
	}
	if err != nil {
		slog.Error("mcp resource read", "user", u.ID, "book", id, "err", err)
		return nil, errInternal
	}

	var doc strings.Builder
	writeBookHeader(&doc, b)
	doc.WriteString("\n---\n\n")

	if err := t.writeBookText(ctx, &doc, b); err != nil {
		slog.Error("mcp resource text", "user", u.ID, "book", b.ID, "err", err)
		return nil, errInternal
	}
	return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
		URI: uri, MIMEType: markdownMIME, Text: doc.String(),
	}}}, nil
}

// writeBookText writes the resource's text section: the text, cut at
// resourceTextCap with a note naming where to continue, or a note saying
// why there is none. Only a failure the model has no words for is
// returned.
func (t tools) writeBookText(ctx context.Context, doc *strings.Builder, b model.Book) error {
	text, more, err := t.d.Text.Read(ctx, b, resourceTextCap)
	if err != nil {
		pending, rerr := textRefusal(b, err)
		if pending {
			doc.WriteString("[" + pendingMsg + " Read this resource again then.]\n")
			return nil
		}
		var ue userError
		if errors.As(rerr, &ue) {
			doc.WriteString("[No text: " + ue.msg + ".]\n")
			return nil
		}
		return err
	}
	doc.WriteString(text)
	if more {
		fmt.Fprintf(doc, "\n\n[The text continues. This document holds the first %d bytes; "+
			"read on with get_book_text(book_id=%q, offset=%d).]\n", len(text), b.ID, len(text))
	}
	return nil
}

// writeBookHeader renders the metadata a reader of the document needs to
// know what it is looking at.
func writeBookHeader(w *strings.Builder, b model.Book) {
	fmt.Fprintf(w, "# %s\n", b.Title)
	if b.Subtitle != "" {
		fmt.Fprintf(w, "\n*%s*\n", b.Subtitle)
	}
	w.WriteString("\n")
	field := func(name, value string) {
		if value != "" {
			fmt.Fprintf(w, "- %s: %s\n", name, value)
		}
	}
	field("Author", b.Author)
	if b.Series != "" {
		series := b.Series
		if b.SeriesIndex > 0 {
			series += " #" + strconv.Itoa(b.SeriesIndex)
		}
		field("Series", series)
	}
	if b.Year > 0 {
		field("Year", strconv.Itoa(b.Year))
	}
	field("Publisher", b.Publisher)
	field("Language", b.Language)
	field("Format", b.Format)
	field("ISBN", b.ISBN)
	if len(b.Tags) > 0 {
		field("Tags", strings.Join(b.Tags, ", "))
	}
	field("book_id", b.ID)
	if d := strings.TrimSpace(b.Description); d != "" {
		w.WriteString("\n" + d + "\n")
	}
}

func displayTitle(b model.Book) string {
	if b.Author == "" {
		return b.Title
	}
	return b.Title + " — " + b.Author
}

func byline(b model.Book) string {
	parts := []string{}
	if b.Author != "" {
		parts = append(parts, b.Author)
	}
	if b.Year > 0 {
		parts = append(parts, strconv.Itoa(b.Year))
	}
	parts = append(parts, b.Format)
	return strings.Join(parts, " · ")
}
