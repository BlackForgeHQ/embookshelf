# MCP server

embookshelf can serve a user's library to assistants that speak the
[Model Context Protocol](https://modelcontextprotocol.io) — Claude
Desktop, Claude Code, Cursor and others. The assistant can search the
library, read book text, and see the user's shelves, highlights and
notes. Everything is read-only. Design: ADR-0039.

## Enabling it

MCP is off by default. An admin turns it on under **Settings → MCP**.
While it is off, `/mcp` answers `503 MCP_DISABLED` to every client and no
one can create a token. Existing tokens are kept and work again when it
is turned back on.

The same tab lists every user's tokens and lets an admin revoke any of
them.

## Connecting a client

Each user creates their own token under **Account → MCP access → New
token**. The dialog shows the token once, with ready-to-paste setup for
Claude Code and a JSON config block, both with the token filled in.

The server URL is your instance's address plus `/mcp`, for example
`https://books.example.com/mcp`. Clients authenticate with
`Authorization: Bearer <token>`.

A token acts as its owner and sees exactly what they see in the web app.
It works only on `/mcp`: it cannot call the regular API, change
settings, or create more tokens.

## Behind a reverse proxy

Nothing special is needed. In particular:

- **Same-host proxies are fine.** The MCP SDK's localhost guard, which
  would refuse Caddy or nginx proxying to `127.0.0.1:6060`, is turned
  off. Every request needs a token instead (ADR-0039 §3).
- **Forward-auth / SSO proxies** (Authelia, oauth2-proxy, Cloudflare
  Access) must let `/mcp` through without their own login, the same way
  `/opds` is usually bypassed: MCP clients can't complete a browser
  login. The token is the authentication.
- **Responses are plain JSON**, not server-sent event streams, so no
  buffering settings are needed.

## Reading book text

`get_book_text`, `search_book_text` and `get_book_toc` read EPUBs
directly. PDFs are read through their Markdown rendition, which needs
the converter extension (`docs/ops/converter.md`). If a PDF has no
up-to-date rendition, the first read asks for one and tells the
assistant to try again shortly. Comics and audiobooks have no text.

- `get_book_toc` lists chapters from the EPUB's table of contents, or the
  headings of a PDF's Markdown, each with the position where it starts.
- `search_book_text` finds a phrase in a book (case-insensitive) and
  returns snippets with positions.

Either position can be passed to `get_book_text` to read from there.

## Books as resources

Clients that attach context (rather than calling tools) can pick books
from the library as resources, `embookshelf://book/<book_id>`. A book
resource is its metadata plus its text, up to about 64k tokens; longer
books end with a note telling the assistant how to read on with
`get_book_text`.
