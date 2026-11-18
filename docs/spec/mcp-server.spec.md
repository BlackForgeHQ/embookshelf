# MCP Server — Feature Specification

> Expose a user's library to MCP clients (Claude Desktop, Claude Code, Cursor, …) so an assistant can search books, read their text, and look at the user's shelves and annotations — as that user, with nothing it couldn't already see in the SPA.

- **Status:** Accepted — see [ADR-0039](../adr/0039-mcp-server-with-personal-access-tokens.md)
- **Scope:** `embookshelf` Go backend (new `internal/mcpserver/`, token auth) + **Settings → MCP** (admin) + **Account → MCP access** (per user)
- **Permission required:** admin to enable MCP and oversee all tokens; any signed-in, approved user to mint their own tokens (tools act as the token's owner)
- **Entry point:** `POST/GET/DELETE /mcp` (Streamable HTTP), authenticated with `Authorization: Bearer <personal access token>`

---

## 1. Purpose

embookshelf already holds the text of most of a user's library (EPUB directly, PDF via the Markdown rendition — ADR-0033). An MCP server lets an assistant use that library as context: "find the book where X", "summarize chapter 3 of Y", "what did I highlight in Z".

Design choices worth flagging up front:

- **In-binary, Streamable HTTP.** Served by the same Gin engine on `:6060` at `/mcp`, through the official `github.com/modelcontextprotocol/go-sdk` (`mcp.NewStreamableHTTPHandler`, wrapped with `gin.WrapH`). No extra process, no second deploy artifact, works for remote clients. A stdio transport is out of scope for v1; the tool set is transport-agnostic so `embookshelf mcp --stdio` can be added later without touching tools.
- **Personal access tokens, not cookies or Basic Auth.** MCP clients store a static credential in their config. Putting the user's password there (Basic, like OPDS) is worse than a revocable, scoped-to-one-purpose token. OAuth 2.1 per the MCP auth spec is the eventual nicer UX but is a much larger build; PATs are the first step and stay useful afterwards for headless clients.
- **Off by default, admin opt-in.** An instance-wide `MCP` setting gates the whole surface. While it is off, `/mcp` answers 503 `MCP_DISABLED` and no token can be minted. Same posture as email and forward-auth.
- **Read-only in v1.** Every v1 tool is a read. Write tools (shelf membership, annotations, metadata edit) are phase 3 and each needs its own decision about write-back side effects (ADR-0001).
- **Tools call services, never handlers or repos directly where a service exists.** Same scoping rules as the HTTP surface: every book lookup goes through the user-scoped `GetByID(ctx, userID, id)` so a token can never reach a book its owner can't.

---

## 2. User Stories

| # | As a … | I want to … | So that … |
|---|--------|-------------|-----------|
| 1 | Reader | Create a named access token in `/account` and copy it once | I can paste it into my MCP client config |
| 2 | Reader | See my tokens with created / last-used dates and revoke any of them | A leaked or forgotten token stops working immediately |
| 3 | Reader | Ask my assistant to search my library | I find books by title, author or topic without opening the SPA |
| 4 | Reader | Ask my assistant about a book's content | It reads the book's text instead of guessing |
| 5 | Reader | Ask my assistant about my highlights and notes | My annotations become searchable context |
| 6 | Admin | Revoke all of a deleted user's tokens implicitly | Deleting a user cuts off their MCP access (FK cascade) |
| 7 | Admin | Turn MCP on or off for the whole instance in Settings → MCP | Nothing is exposed until I choose to expose it, and I can cut every client off at once |
| 8 | Admin | See every user's tokens (name, owner, created, last used) and revoke any of them | I can respond to a leak without waiting on the user |
| 9 | Reader | Copy a ready-to-paste setup command for my client from Account → MCP access, with my new token already filled in | Connecting takes one paste, not reading docs |

---

## 3. Personal access tokens

### 3.1 Schema — migration `000047_api_tokens`

```sql
CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,     -- sha256(raw token)
    prefix       text NOT NULL,             -- first 8 chars after "ebk_", for display
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz                -- NULL = never
);
CREATE INDEX api_tokens_user_id_idx ON api_tokens(user_id);
```

- **Format:** `ebk_` + 32 random bytes, base64url, no padding. The prefix makes tokens greppable by secret scanners.
- **Storage:** only `sha256(token)` is stored. A high-entropy random token doesn't need bcrypt; a lookup is one indexed equality. The raw token is returned exactly once, from the create call.
- **Not ADR-0010 material:** the hash isn't a secret to decrypt, so no AES-GCM.
- `last_used_at` is bumped at most once a minute per token (`WHERE last_used_at IS NULL OR last_used_at < now() - interval '1 minute'`) so a chatty client doesn't write on every call.
- `sqliteimport`: add `api_tokens` to `excludedTables` with reason "introduced after the SQLite backend was retired" so the parity test stays green.

### 3.2 Service + API

`service.APITokenService` — `Create(ctx, userID, name, expiresAt) (raw string, model.APIToken, error)`, `List(ctx, userID)`, `ListAll(ctx)` (admin, joined with owner email/name), `Revoke(ctx, userID, id)`, `RevokeAny(ctx, id)` (admin), `UserByToken(ctx, raw) (model.User, error)`.

`UserByToken` rejects expired tokens and users whose approval status isn't approved, the same gate the session path enforces. `Create` refuses with `ErrMCPDisabled` while the `MCP` setting is off.

Per-user (cookie-authed, `authed` group):

| Method | Path | Body / response |
|--------|------|-----------------|
| `GET` | `/api/v1/account/tokens` | `[{id, name, prefix, createdAt, lastUsedAt, expiresAt}]` |
| `POST` | `/api/v1/account/tokens` | `{name, expiresInDays?}` → `{token, ...meta}` (raw token present only here); 503 `MCP_DISABLED` while off |
| `DELETE` | `/api/v1/account/tokens/:id` | 204; 404 for another user's token id |

Admin (`/settings` group, `RequireRole(admin)`):

| Method | Path | Body / response |
|--------|------|-----------------|
| `GET` | `/api/v1/settings/mcp` | `{enabled}` |
| `PUT` | `/api/v1/settings/mcp` | `{enabled}`; a settings-surface declaration (`handler.settingsDomain`) whose post-save side effect publishes the flag to the runtime holder |
| `GET` | `/api/v1/settings/mcp/tokens` | `[{id, name, prefix, owner: {id, email, name}, createdAt, lastUsedAt, expiresAt}]` |
| `DELETE` | `/api/v1/settings/mcp/tokens/:id` | 204; revokes any user's token |

A token cannot mint more tokens or reach any of these routes: the token middleware is mounted only on `/mcp`.

### 3.2a The `MCP` setting

`repo.Setting[MCPConfig]` keyed `MCP`, `MCPConfig{Enabled bool}`, default `Enabled: false`, added to `settingsRegistry`. Read once at boot into an `atomic.Pointer` holder (the forward-auth shape) and republished by the settings PUT, so the per-request gate on `/mcp` is a pointer load, not a DB read.

Turning MCP off does **not** delete tokens. It gates them: `/mcp` answers 503 `MCP_DISABLED` before the token is even resolved, and existing tokens work again when it's turned back on. To kill tokens for good, the admin revokes them.

### 3.3 Middleware

`auth.BearerToken(resolver TokenResolver)` in `internal/auth/` — reads `Authorization: Bearer ebk_…`, resolves, `WithUser` onto the context. Missing or invalid answers `401` with `WWW-Authenticate: Bearer realm="embookshelf"`.

**CSRF:** `CSRFGuard` runs globally and rejects a POST with no `Origin`, which every non-browser MCP client sends. `/mcp` is exempt from it the same way forward-auth is: CSRF exploits ambient credentials (cookies), and a bearer header is not ambient. The exemption is keyed on the route, and `/mcp` never accepts the session cookie, so the exemption can't be ridden with a cookie.

**DNS rebinding:** the MCP spec asks servers to validate `Origin` when present. Keep the SDK's own origin and localhost protection on, and additionally reject a present `Origin` that isn't in `AllowedOrigins` or same-host.

### 3.4 UI

The endpoint URL shown in both panels is `window.location.origin + "/mcp"`: the server has no configured public URL, and the browser's origin is the one users reach.

**Settings → MCP** (new row in `SETTINGS_SECTIONS`, key `mcp`, `adminOnly: true`, panel `ui/src/components/settings/McpPanel.tsx`), in two blocks:

1. **Server.** Enable switch; when on, the endpoint URL with a copy button.
2. **Tokens.** Every user's tokens: name, owner, prefix, created, last used, expires, and a revoke button. Empty state when there are none.

**Account → MCP access** (new row in the `/account` `SECTIONS`, key `mcp`, panel `ui/src/components/account/McpAccessPanel.tsx`). While MCP is off it shows "Your administrator hasn't enabled MCP", and nothing else. While on, it has three blocks:

1. **Connecting a client.** Ready-to-paste snippets, each with a copy button:
   - Claude Code: `claude mcp add --transport http embookshelf <url> --header "Authorization: Bearer <token>"`
   - Generic JSON `mcpServers` block (Claude Desktop, Cursor, and other clients that take the standard config shape)

   The snippets carry a `<token>` placeholder by default.
2. **Your tokens.** The user's own tokens (name, prefix, created, last used, expires) with revoke. Revoke uses a plain confirm rather than `ConfirmPhraseDialog`, because revoking is cheap to undo by minting a new token.
3. **New token** dialog (name, expiry: never / 30 / 90 / 365 days). On success it shows the raw token once with a copy button and the same snippets with the real token filled in, so the user copies one finished command. Closing the dialog drops the raw token from memory; the snippets in block 1 go back to the placeholder.

The account panel learns whether MCP is on from `GET /api/v1/config` (add `mcpEnabled`), which every signed-in user can already read. One API module, `ui/src/api/mcp.ts`, holds both sides as `defineQuery` / `defineMutation` specs (a token minted or revoked on one side invalidates the other's list), plus the endpoint and snippet builders both panels share.

---

## 4. MCP server

### 4.1 Package layout

```
internal/mcpserver/
├── server.go        # New(deps) *mcp.Server; Handler() (stateless Streamable HTTP); withUser seam
├── deps.go          # narrow interfaces over the services it needs (testable with fakes)
├── tools_catalog.go # search_library, list_libraries, list_shelves, list_shelf_books, get_book
├── tools_text.go    # get_book_text, search_book_text, get_book_toc; textRefusal
├── tools_notes.go   # list_annotations, get_reading_guide
├── resources.go     # embookshelf://book/{book_id}; per-user resources/list
└── *_test.go        # a real Streamable HTTP client against Handler()
```

Wired in `internal/app.Build` and handed to the handler as `handler.MCPDeps` (switch, token service, server). Mounted in `handler/router.go`:

```go
mcpGroup := r.Group(mcpPath)
mcpGroup.Use(
	h.requireMCPEnabled,                           // 503 MCP_DISABLED, before any token lookup
	auth.PresentOriginGuard(h.cfg.AllowedOrigins), // a present, foreign Origin → 403
	auth.BearerToken(h.tokenResolver()),           // fails closed without a token service
)
mcpGroup.Any("", h.MCP)
```

The enabled gate runs first so a disabled instance never touches `api_tokens` and never bumps `last_used_at`. `CSRFGuard` exempts exactly `mcpPath`.

The transport is **stateless** (ADR-0039 §3): no session table, GET answers 405, JSON responses. The SDK's localhost DNS-rebinding guard is off, because it refuses a same-host reverse proxy; the bearer requirement plus the Origin guard cover the attack.

The user reaches a tool call through the SDK's `TokenInfo`: `Handler` wraps the SDK's `RequireBearerToken` with a verifier that restamps the user `auth.BearerToken` already resolved. A tool reads it from `req.Extra.TokenInfo`, never from the request context. One helper, `withUser(fn)`, wraps every tool so a body can't run without a user. It is the MCP equivalent of `userScoped`, and `lookupBook` is the equivalent of `bookScoped`: validate the UUID (a malformed id never reaches Postgres), resolve as the user, map not-found.

Server info: name `embookshelf`, version from the build's version string. `instructions` gives a short paragraph telling the model to search first, then read text in pages, and that book ids are UUIDs.

### 4.2 v1 tools (all read-only, `readOnlyHint: true`)

| Tool | Input | Output | Backed by |
|------|-------|--------|-----------|
| `search_library` | `query`, `library?`, `format?`, `limit?` (default 20, ≤50), `offset?` | `total`, a page of books (id, title, author, series, year, format, language, library id, progress); on the first page also the user's shelves and the libraries whose names match | book store `Search` (tsvector, `websearch_to_tsquery` syntax) + `SearchService.Suggest` for shelves/libraries |
| `list_libraries` | — | id, name, slug, book count | `LibraryService.List` |
| `list_shelves` | — | slug, name, kind (regular / smart / shared), book count | `ShelfService.List` |
| `list_shelf_books` | `slug`, `sort?` | books | `ShelfService.Books` |
| `get_book` | `book_id` | full metadata: title, authors, series, description, ISBN, tags, language, format, pages, reading progress, shelves | book store `GetByID(userID, id)` + `ShelfService.SlugsForBook` + progress |
| `get_book_text` | `book_id`, `offset?` (default 0), `max_chars?` (default 20 000, max 50 000) | `{status: ok \| pending, message?, text, offset, next_offset \| null}` | `service.BookText.Page`, see §4.3 |
| `search_book_text` | `book_id`, `query`, `max_results?` (default 20, ≤100) | `{status, total, total_capped?, partial?, matches: [{offset, snippet}]}` | `service.BookText.Search`, see §4.3a |
| `get_book_toc` | `book_id` | `{status, entries: [{title, level?, offset}], truncated?}` (≤500 entries) | `service.BookText.Outline`, see §4.3a |
| `list_annotations` | `book_id?`, `limit?` | highlights and notes, each with book id, text, note, color, created at | `AnnotationService.ListForBook` / `ListRecent` |
| `get_reading_guide` | `book_id` | the guide (about / audience / not-for / problems) or "none generated" | reading guide repo read (no generation, since generating spends the admin's LLM key) |

Outputs are typed Go structs so the SDK publishes an output schema and returns `structuredContent` alongside the text content.

### 4.3 Book text

`get_book_text` reads through `service.BookText`, the source rules that used to live in `ReadingGuideService.readSource`. The guide now reads through it too, so the two can't diverge. `BookText` reports failures; each caller decides what they mean.

Offsets count bytes of UTF-8 text. `Page` moves both edges onto character boundaries (a start inside a character moves forward), so every page is valid text and the `next_offset` chain reassembles the book exactly. `max_chars` is therefore a byte budget, close to a character count for Latin text.

- **EPUB:** `fileproc.ExtractEPUBText`, read up to `offset + max_chars + 1` bytes (the extra byte tells "exactly this long" from "longer") and sliced. The cost is O(offset) per page, which is acceptable for v1. Chapter-indexed reads via `ExtractEPUBSegments` are a follow-up (§7).
- **Convertible (PDF):** `MarkdownFeed.Text`. A missing or stale rendition **is requested** (the feed's existing behavior, kept on purpose: ADR-0039 §4), and the tool answers with a result (not a protocol error) saying the text is being prepared and to retry in a minute. `ErrRenditionPending` is mapped explicitly. A failed rendition returns its verbatim message. With no converter configured, the answer is the feed's "converter extension is not configured" message, unchanged.
- **CBZ / audio / MOBI / FB2:** a tool error, "no readable text for format X". No silent metadata fallback, because the model should know it didn't read the book.

### 4.3a Search and table of contents

Both return offsets in the same coordinates as `get_book_text`, so the model can jump straight to a passage or a chapter. Both share `get_book_text`'s refusal mapping (`textRefusal`): pending is a `pending` result; a failed conversion, a textless format and an image-only EPUB are tool errors with the same messages.

- **Search** reads up to 32 MiB of text (far past any novel; `partial` says when a book is longer). The query's words match in order, case-insensitively, with any whitespace between them. That makes it a phrase search that survives line breaks. It matches substrings, unstemmed and unranked. Regex metacharacters are literal. Matches are counted up to 10,000 (`total_capped` past that); snippets carry about 160 bytes of context either side, on character boundaries, marked with … where cut.
- **TOC, EPUB:** `fileproc.ExtractEPUBOutline`, titles from the nav document or NCX (`tocTitles`, the same map narration uses) at spine-document granularity. Documents the TOC doesn't name are left out, because books often split one chapter across files. If the TOC names none, every document is listed as "Section N". The outline's offsets come from the *same walk* that produces the text (`walkEPUBText`), so they cannot drift from what `get_book_text` serves. Entries have no level: the TOC is read flat.
- **TOC, PDF:** the Markdown rendition's ATX headings (`#`–`######`), with level. Fenced and indented code is skipped; setext headings are not read (converters emit ATX, and `---` is usually a rule).

### 4.3b Book resources

Each book is an MCP resource, `embookshelf://book/{book_id}` (a resource template), for clients that attach context rather than call tools.

- **Read** returns one Markdown document: title, subtitle, author, series, year, publisher, language, format, ISBN, tags, `book_id`, description, then `---` and the text. The text is capped at 256 KiB (about 64k tokens); past that, a closing note names the `get_book_text` call that continues it. A book whose text can't be had still reads: the metadata is kept and a note says why there's no text (pending, textless format, failed conversion). Only an unknown book or malformed id is an error (`ResourceNotFoundError`).
- **List** (`resources/list`) is the user's library by title, 100 per page, with an offset cursor. The SDK answers `resources/list` from resources registered at startup, the same for everyone. A library is per user, so a receiving middleware answers the method per request from the token's user.

### 4.4 Errors

- Unknown book, or a `book_id` that isn't a UUID → tool result with `isError: true`, "book not found". `repo.ErrNotFound` is the only branch; same rule as `writeBookLookupError`. Unknown shelf → "shelf not found".
- Anything else → `isError: true`, a generic message; the error itself is logged with `slog`, including the tool name and user id.
- Protocol-level errors are reserved for malformed input, which the SDK's schema validation already handles.

### 4.5 Observability

When `OTEL_ENABLED`, wrap `/mcp` in `otelgin` like the rest of the engine; add a span attribute `mcp.tool` via SDK receiving middleware. No new metrics in v1.

---

## 5. Phasing

1. **Tokens + MCP setting.** Migration, repo, service, `MCP` setting + holder, account and admin routes, bearer middleware, Settings → MCP tab, Account → MCP access tab, tests. Reviewable alone (nothing consumes tokens yet).
2. **MCP read-only.** `internal/mcpserver`, the first eight tools, the `BookText` extraction refactor, router mount, docs (`docs/architecture.md` gets the fifth surface; `docs/ops/mcp.md` covers enabling, connecting a client and reverse proxies). The client config snippets live in Account → MCP access.
2b. **Finding things inside a book.** `search_book_text`, `get_book_toc`, and books as resources (§4.3a, §4.3b).
3. **Writes (separate spec).** `add_to_shelf` / `remove_from_shelf`, `create_annotation`, maybe `update_progress`. Metadata editing stays out until we decide how an agent edit interacts with locks and sidecar write-back.

---

## 6. Testing

- `repo`: `api_tokens` CRUD against `repotest` schema; hash lookup; expiry; `last_used_at` throttle.
- `service`: `UserByToken` rejects expired, unknown, and unapproved users; `Create` refuses while MCP is off.
- `repo`: `MCP` row appears in `settingsRegistry` (the existing parity test catches a missing entry).
- `auth`: `BearerToken` 401 shape + `WWW-Authenticate`; CSRF exemption only on `/mcp`; cookie alone on `/mcp` → 401.
- `handler`: router role test covers `/api/v1/account/tokens` (authed), `/api/v1/settings/mcp*` (admin only, non-admin 403), and `/mcp` (token only); `/mcp` with a valid token answers 503 while disabled and works after the PUT flips it on (holder republish); admin revoke of another user's token makes that token 401.
- `mcpserver`: a real Streamable HTTP client against `Handler()`; each tool against fakes; every catalog read is made as the token's user; a request with no resolved user is a 401; a malformed `book_id` never reaches the store; infrastructure errors reach the client only as "internal error"; `get_book_text`'s `next_offset` chain reassembles the full text; pending rendition is a `pending` result, not an error, and each refusal maps to its message.
- `service`: `BookText` pages multibyte text into valid UTF-8 pages that reassemble exactly, moves a mid-character start forward, requests a missing rendition and reports pending; the reading-guide tests pass unchanged through the extracted source.
- `handler` (`/mcp`): a valid token with no Origin reaches `initialize`; disabled answers 503 before any token lookup; missing, unknown and cookie-only credentials are 401; a foreign Origin is 403 before the token lookup; the CSRF exemption covers `/mcp` exactly; no token service fails closed.
- `sqliteimport`: exclusion parity test passes with `api_tokens` excluded.
- UI: vitest for both panels. Account: create shows the token once; the snippets fill in the new token and revert to the placeholder when the dialog closes; revoke removes the row; the disabled state hides everything but the notice. Settings: the toggle round-trips; the token table lists owners; revoke removes the row.

---

## 7. Decisions and follow-ups

Resolved (2026-11-18), recorded in ADR-0039:

1. **Instance kill switch:** yes, Settings → MCP, off by default, plus an admin view of every token.
2. **May a read trigger a PDF conversion:** yes, same as guide generation.
3. **Text addressing:** offsets in v1 (UTF-8 bytes, page edges on character boundaries).

Follow-ups, not in v1:

- **`chapter` option on `get_book_text`.** Deferred (2026-11-18). `get_book_toc` already gives each chapter's offset, so "read chapter 3" works today in two calls.
- **`get_reading_stats`.** Deferred (2026-11-18).
- **OAuth 2.1** per the MCP authorization spec. The go-sdk ships `auth` helpers. embookshelf would either act as the authorization server or delegate to the configured OIDC provider.
- **stdio transport** (`embookshelf mcp --stdio`) for clients that can't do HTTP.
- **Write tools.** See §5 phase 3. Deferred (2026-11-18).
