# The MCP server lives in the binary at `/mcp`, authenticates with personal access tokens, and is off until an admin turns it on

Users want their assistant (Claude Desktop, Claude Code, Cursor, …) to use their library as context: find a book, read it, see their highlights. The Model Context Protocol is how those clients take external tools. This ADR settles where the server runs, how its clients authenticate, who controls whether it exists at all, and what a read may cost. The feature itself is specified in [`docs/spec/mcp-server.spec.md`](../spec/mcp-server.spec.md).

## Status

accepted (2026-11-18)

## Decisions bundled here

### 1. Streamable HTTP inside the binary, at `/mcp`, through the official Go SDK

Served by the same Gin engine as `/api/v1`, `/opds` and `/events`, via `github.com/modelcontextprotocol/go-sdk`'s `StreamableHTTPHandler` mounted with `gin.WrapH`. A fifth surface, not a second process: the single-binary property holds, remote clients work, and every tool reaches the same services the HTTP handlers call. That means the same user-scoped `GetByID`, so a token can never reach a book its owner can't see.

stdio (`embookshelf mcp --stdio`) was rejected for v1. It only serves a client on the same host as `DATABASE_URL`, which is rarely where a self-hosted library and a user's laptop meet. Tools are written transport-agnostic, so it remains a cheap follow-up.

The community `mark3labs/mcp-go` was rejected in favour of the SDK maintained by the protocol's own project. Spec conformance (auth, origin checks, protocol revisions) is the part we least want to own.

### 2. Personal access tokens, not cookies, Basic Auth or (yet) OAuth

An MCP client holds one static credential in a config file. The options were:

- **Session cookie:** not a thing a config file can hold, and it expires on a 7-day sliding window.
- **HTTP Basic, as OPDS does:** puts the user's password in a plaintext config, and for an OIDC- or forward-auth-only user there is no password at all.
- **OAuth 2.1 per the MCP authorization spec:** the right long-term UX, but it makes embookshelf an authorization server (registration, consent, refresh) or demands a delegation story per OIDC provider. Deferred, not rejected.
- **Personal access tokens:** chosen. Revocable one at a time, purpose-named, work for every identity type, and stay useful for headless clients after OAuth lands.

The token shape: `ebk_` + 32 random bytes (base64url). The prefix is for secret scanners. Only `sha256(token)` is stored, in a new `api_tokens` table (`ON DELETE CASCADE` from `users`). bcrypt is unnecessary for 256 bits of entropy and would turn an indexed lookup into a scan. This is a one-way hash, not a secret to decrypt, so ADR-0010's AES-GCM does not apply. The raw token is shown once, at creation. Tokens resolve through the same approval gate as sessions: an unapproved or denied user's token is refused.

A token authenticates **only** `/mcp`. The bearer middleware is mounted on that group alone, so a token cannot reach `/api/v1`, mint more tokens, or touch settings, whatever role its owner has.

### 3. `/mcp` is exempt from CSRFGuard, and accepts no cookie

`CSRFGuard` refuses a state-changing request without `Origin`/`Referer`, and no non-browser MCP client sends one. CSRF works by riding *ambient* credentials, meaning cookies a browser attaches on its own. A bearer header is never ambient, so the origin check protects nothing on this route. The exemption is keyed on the `/mcp` route, and `/mcp` deliberately does not accept the session cookie. Without both halves, a cross-site POST from a signed-in browser could ride the exemption. This is the same argument ADR-0022 made for forward-auth requests, with a different provenance (a header the attacker can't make the browser send, instead of a trusted source IP).

The MCP spec's DNS-rebinding rule still holds: an `Origin` that *is* present must match the host or `AllowedOrigins` (`auth.PresentOriginGuard`, sharing `CSRFGuard`'s matcher).

The SDK's own localhost guard is **off**. It refuses any request that reaches a loopback listener with a non-loopback `Host`, which is exactly what Caddy or nginx on the same machine sends when proxying to `127.0.0.1:6060`, the most common self-hosted shape. The attack it defends against (a rebinding page reaching a local server) needs an ambient credential to do harm, and this route has none: a bearer token is required on every request, and the page cannot know it. The Origin check above covers the rest.

The transport is **stateless** Streamable HTTP: every tool is a read that needs nothing from a previous call, so there is no session table to hold in memory or lose on restart. The authenticated user reaches each tool call through the SDK's `TokenInfo` (stamped from the user `auth.BearerToken` resolved), not through the request context, which the SDK does not promise to hand a tool handler.

### 4. An instance-wide switch, off by default, with admin oversight of every token

A new `MCP` `Setting[T]` (`{Enabled bool}`, default false), registered in the settings registry, edited in a new **Settings → MCP** tab. Off means:

- `/mcp` answers 503 `MCP_DISABLED` *before* any token is resolved, so a disabled instance never reads `api_tokens`;
- token creation refuses with the same code; and
- existing tokens are kept, not deleted. Turning MCP back on restores them. Revocation is a separate, explicit act.

The flag is held in an atomic holder published at boot and on save (the forward-auth shape), so the per-request gate is a pointer load.

The same tab lists every user's tokens (owner, name, prefix, created, last used, expiry) with revoke. Users mint and revoke their own tokens under **Account → MCP access**. Settings is an admin hub, and token ownership is per user.

Default-off was chosen over "no tokens means no access". The second is true, but it leaves the admin unable to see or stop exposure they didn't opt into. Email delivery and forward-auth already set the precedent that an instance's external surfaces are opt-in.

### 5. A read may request a PDF conversion

`get_book_text` on a Convertible-format book reads its Markdown rendition through `MarkdownFeed`. A missing or stale rendition is **requested**, and the tool answers "being prepared, retry shortly". It does not hand back metadata as though it were the book. This matches reading-guide generation and keeps ADR-0033 §5's loud-failure rule: text one conversion away is never silently replaced by nothing.

This is a deliberate exception to ADR-0024 §4's cost-follows-explicit-action rule. The cost here is converter CPU on the operator's own sidecar, not a metered third-party key, and it is paid once per book's bytes, not once per read. Anything that spends money (guide generation, narration) stays out of the MCP surface: `get_reading_guide` reads an existing guide and never generates one.

### 6. Read-only in v1; text is paged by character offset

Every v1 tool is a read. Writes (shelf membership, annotations, progress, and especially metadata edits with their lock and sidecar write-back semantics under ADR-0001) each need their own decision and arrive in a later spec.

`get_book_text` pages by offset (`offset`, `max_chars` ≤ 50 000, `next_offset`), which fits any client's context budget without a table-of-contents round trip. Offsets count bytes of UTF-8 text, and both page edges are moved onto character boundaries, so a page is always valid text and pages reassemble exactly. A client is expected to pass `next_offset` back rather than do arithmetic. `get_book_toc` and `search_book_text` hand out offsets in the same coordinates, so a client jumps to a chapter or a passage by passing one to `get_book_text`. A `chapter` parameter on `get_book_text` itself stays a follow-up.

Books are also MCP resources (`embookshelf://book/{book_id}`), for clients that attach context rather than call tools. A resource is capped at 256 KiB of text and says where to continue. `resources/list` is answered per user by middleware, because the SDK's list is the same for every caller.

Where a book's text comes from is stated once, in `service.BookText`, which reading guides and MCP both read through. It reports failures and does not decide what they mean: an unreadable EPUB becomes a metadata-only guide, but MCP tells the model it could not read the book.

## Consequences

Positive:

- One binary, one deploy. An admin flips a switch, a user mints a token, a client connects.
- A leaked token is contained twice over: it reaches only read tools as one user, and the owner or an admin can revoke it alone.
- The kill switch cuts every client off at once without destroying anyone's setup.

Negative:

- A bearer token in a client config is a long-lived secret on someone's laptop. Optional expiry and `last_used_at` visibility mitigate this, but don't solve it. OAuth is the real fix.
- EPUB paging by offset re-extracts from the start on every page (O(offset)). Fine at book scale; revisit if profiles say otherwise.
- `/mcp` is now a second CSRF exemption. Anyone widening it, by accepting cookies on `/mcp` or exempting another route by the same argument, must re-read §3.

Neutral:

- `api_tokens` postdates the SQLite backend, so `sqliteimport` lists it in `excludedTables` with that reason.
- OPDS keeps Basic Auth; e-readers aren't MCP clients.
