// SPDX-License-Identifier: AGPL-3.0-or-later

package mcpserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/model"
	"github.com/blackforge/embookshelf/internal/repo"
)

const (
	defaultAnnotationLimit = 50
	maxAnnotationLimit     = 200
)

type annotationsIn struct {
	BookID string `json:"book_id,omitempty" jsonschema:"one book's annotations; omit for the most recent across all books"`
	Limit  int    `json:"limit,omitempty" jsonschema:"at most this many, default 50, at most 200"`
}

type annotationOut struct {
	ID        string `json:"annotation_id"`
	BookID    string `json:"book_id"`
	Text      string `json:"highlighted_text,omitempty"`
	Note      string `json:"note,omitempty"`
	Color     string `json:"color,omitempty"`
	CreatedAt string `json:"created_at"`
}

type annotationsOut struct {
	Annotations []annotationOut `json:"annotations"`
}

func (t tools) listAnnotations(ctx context.Context, u model.User, in annotationsIn) (annotationsOut, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = defaultAnnotationLimit
	}
	limit = min(limit, maxAnnotationLimit)

	var (
		list []model.Annotation
		err  error
	)
	if strings.TrimSpace(in.BookID) != "" {
		b, lerr := t.lookupBook(ctx, u, in.BookID)
		if lerr != nil {
			return annotationsOut{}, lerr
		}
		list, err = t.d.Annotations.ListForBook(ctx, u.ID, b.ID)
	} else {
		list, err = t.d.Annotations.ListRecent(ctx, u.ID, limit)
	}
	if err != nil {
		return annotationsOut{}, err
	}
	if len(list) > limit {
		list = list[:limit]
	}
	out := annotationsOut{Annotations: make([]annotationOut, 0, len(list))}
	for _, a := range list {
		out.Annotations = append(out.Annotations, annotationOut{
			ID: a.ID, BookID: a.BookID, Text: a.SelectedText, Note: a.Note, Color: a.Color,
			CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

type guideOut struct {
	Available bool   `json:"available" jsonschema:"false when no guide has been generated for this book"`
	About     string `json:"about,omitempty"`
	Audience  string `json:"audience,omitempty"`
	NotFor    string `json:"not_for,omitempty"`
	Problems  string `json:"problems,omitempty"`
	Source    string `json:"source,omitempty" jsonschema:"full_text when written from the book itself, metadata when only from its description"`
}

func (t tools) getReadingGuide(ctx context.Context, u model.User, in bookIDIn) (guideOut, error) {
	b, err := t.lookupBook(ctx, u, in.BookID)
	if err != nil {
		return guideOut{}, err
	}
	// Read only. Generating spends the admin's LLM key, which is not this
	// surface's to spend (ADR-0039 §5).
	g, err := t.d.Guides.GetByBookID(ctx, b.ID)
	if errors.Is(err, repo.ErrNotFound) {
		return guideOut{Available: false}, nil
	}
	if err != nil {
		return guideOut{}, err
	}
	return guideOut{
		Available: true,
		About:     g.About,
		Audience:  g.Audience,
		NotFor:    g.NotFor,
		Problems:  g.Problems,
		Source:    string(g.SourceKind),
	}, nil
}

func (t tools) registerNotes(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_annotations",
		Description: "List the user's highlights and notes, for one book or the most recent across all books.",
		Annotations: readOnly("List annotations"),
	}, withUser("list_annotations", t.listAnnotations))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_reading_guide",
		Description: "Get a book's reading guide: what it is about, who it is for and not for, and the problems it addresses. Only returns guides that already exist.",
		Annotations: readOnly("Get reading guide"),
	}, withUser("get_reading_guide", t.getReadingGuide))
}
