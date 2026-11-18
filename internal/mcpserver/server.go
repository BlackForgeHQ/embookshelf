// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mcpserver is embookshelf's Model Context Protocol surface
// (ADR-0039): read-only tools over a user's library, served as stateless
// Streamable HTTP at /mcp.
//
// Every tool acts as the user whose access token authenticated the
// request, through the same user-scoped services the HTTP API calls — a
// token reaches nothing its owner could not see in the SPA.
package mcpserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/blackforge/embookshelf/internal/auth"
	"github.com/blackforge/embookshelf/internal/model"
)

const instructions = `embookshelf is the user's personal ebook library.
Start with search_library (or list_shelves / list_shelf_books) to find books; every book is addressed by its book_id.
get_book returns metadata, reading progress and shelves. get_book_text returns the book's text one page at a time: pass next_offset back as offset to continue, until next_offset is null. To go straight to a part of a book, take an offset from get_book_toc (chapters) or search_book_text (mentions). If a text tool reports status "pending", the text is being prepared; try again in a minute.
Each book is also a resource, embookshelf://book/{book_id}: its metadata and text in one document, for attaching as context.
list_annotations returns the user's highlights and notes. get_reading_guide returns a short orientation for a book, when one exists.
Everything is read-only.`

// New builds the MCP server with every tool registered.
func New(d Deps) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "embookshelf",
		Title:   "embookshelf",
		Version: d.Version,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		// Tools only: no logging capability to advertise.
		Capabilities: &mcp.ServerCapabilities{},
	})
	t := tools{d: d}
	t.registerCatalog(s)
	t.registerText(s)
	t.registerNotes(s)
	t.registerResources(s)
	return s
}

// Handler serves s as stateless Streamable HTTP.
//
// It expects to run behind auth.BearerToken, which has already resolved
// the token to a user on the request context. That user is restamped as
// the SDK's TokenInfo, which is what the SDK hands to each tool call in
// CallToolRequest.Extra — the request context itself is not guaranteed to
// reach a tool handler.
//
// Stateless because every tool is a read that needs nothing from a
// previous call: no session table to hold in memory, nothing lost on a
// restart, and a GET for a server-push stream answers 405.
//
// The SDK's localhost DNS-rebinding guard is off. It refuses any request
// that reaches a loopback listener with a non-loopback Host — exactly
// what a reverse proxy on the same host sends. The attack it defends
// against needs an ambient credential; this route has none (a bearer
// token is required on every request), and present Origin headers are
// checked upstream (ADR-0039 §3).
func Handler(s *mcp.Server) http.Handler {
	h := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{
			Stateless:                  true,
			JSONResponse:               true,
			DisableLocalhostProtection: true,
		},
	)
	return sdkauth.RequireBearerToken(tokenInfoFromUser, &sdkauth.RequireBearerTokenOptions{
		// Expiry is enforced where the token was resolved; an
		// expired token never gets this far.
		AllowMissingExpiration: true,
	})(h)
}

// userKey is where the resolved user rides in TokenInfo.Extra.
const userKey = "embookshelf.user"

// tokenInfoFromUser is the SDK verifier. It verifies nothing itself: the
// token was resolved by auth.BearerToken upstream, and this only carries
// that answer into the SDK's own vocabulary. No user on the context means
// the handler was mounted without the middleware — refuse.
func tokenInfoFromUser(_ context.Context, _ string, r *http.Request) (*sdkauth.TokenInfo, error) {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return nil, sdkauth.ErrInvalidToken
	}
	return &sdkauth.TokenInfo{
		UserID: u.ID,
		Extra:  map[string]any{userKey: *u},
	}, nil
}

// userOf reads the authenticated user off a request's transport extras —
// a tool call's, a resource read's, or a list request's.
func userOf(extra *mcp.RequestExtra) (model.User, bool) {
	if extra == nil || extra.TokenInfo == nil {
		return model.User{}, false
	}
	u, ok := extra.TokenInfo.Extra[userKey].(model.User)
	return u, ok && u.ID != ""
}

// userError is a failure whose message is the model's to read: "book not
// found", "no readable text for CBZ". Everything else is ours, logged and
// answered as a generic internal error so no SQL or path reaches a client.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func failf(msg string) error { return userError{msg: msg} }

var errInternal = errors.New("internal error")

// withUser is the user-scoped seam for tools — the MCP twin of the
// handler tier's userScoped. A tool body cannot run without a user, so a
// tool registered through it fails closed by construction.
func withUser[In, Out any](name string, fn func(context.Context, model.User, In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		var extra *mcp.RequestExtra
		if req != nil {
			extra = req.Extra
		}
		u, ok := userOf(extra)
		if !ok {
			slog.Error("mcp tool called without a user", "tool", name)
			return nil, zero, errInternal
		}
		out, err := fn(ctx, u, in)
		if err != nil {
			var ue userError
			if errors.As(err, &ue) {
				return nil, zero, ue
			}
			slog.Error("mcp tool", "tool", name, "user", u.ID, "err", err)
			return nil, zero, errInternal
		}
		return nil, out, nil
	}
}

// readOnly is every v1 tool's annotation (ADR-0039 §6).
func readOnly(title string) *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: &f,
		IdempotentHint:  true,
		OpenWorldHint:   &f,
	}
}
