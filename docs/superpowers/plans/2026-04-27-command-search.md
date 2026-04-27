# Command-Powered Search & Palette Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the shadcn `Command` (cmdk) primitive to the SPA and use it for two surfaces: (1) an inline library-page search combobox that keeps the existing grid filter and adds a "jump to book" popover, and (2) a global ⌘K palette that searches books / shelves / libraries and runs navigation + quick-action verbs.

**Architecture:** Backend gets a new lightweight `GET /api/v1/search` endpoint that fans out three slim suggest queries in parallel via `errgroup`. Frontend adds the shadcn `Command` primitive plus two consumer components (`LibrarySearchCombobox`, `CommandPalette`), a `ShelfDraftProvider` that mirrors the existing `UserSettingsDialogProvider` pattern, and a `useLogout` hook so Sidebar and CommandPalette share one logout side-effect chain.

**Tech Stack:** Go 1.25 + Gin + pgx + golang.org/x/sync/errgroup; React 19 + TanStack Router/Query + Tailwind 4 + cmdk + Radix Popover/Dialog (already vendored via `radix-ui`).

## Spec adjustments

These deviate from `docs/superpowers/specs/2026-04-27-command-search-design.md`. Reasons live next to the task that applies them.

1. **No Zustand.** The spec proposed a `commandStore`. The codebase already established a Context+Provider pattern (`UserSettingsDialogProvider` in `_app.tsx`) for the same problem. We mirror that pattern for the create-shelf dialog (`ShelfDraftProvider`) and drop Zustand entirely — fewer deps, consistent style.
2. **Reuse existing `UserSettingsDialogProvider`.** It already exposes `useUserSettingsDialog().open()`. CommandPalette consumes it directly; no Sidebar refactor needed for that dialog.
3. **No Go endpoint tests in this PR.** The repo has zero handler/service/repo test files — only pure-function unit tests in `crypto`, `provider`, `pattern`. Adding a test DB harness for one new endpoint would balloon scope. Coverage comes from frontend tests + the new Playwright spec + the existing CI compile/lint pass. Filed as follow-up TODO at the end of this plan.

## File structure

### Backend — new

- `internal/repo/library.go` — append `LibraryRepo.SearchSuggestBooks` and `LibraryRepo.SearchSuggestLibraries`.
- `internal/repo/shelf.go` — append `ShelfRepo.SearchSuggest`.
- `internal/service/search.go` — new `SearchService` orchestrating the three repo calls in parallel.
- `internal/handler/search.go` — new `(h *Handler) Search` handler.

### Backend — modified

- `internal/handler/handler.go` — add `search *service.SearchService` field and `Search *service.SearchService` to `Deps` + wire in `New(d Deps)`.
- `internal/handler/router.go` — register `authed.GET("/search", h.Search)`.
- `cmd/embookshelf/main.go` — instantiate `searchSvc := service.NewSearchService(libRepo, shelfRepo)` and pass to `handler.New`.
- `go.mod` / `go.sum` — pull in `golang.org/x/sync/errgroup` (likely already transitively present; explicit `go get` if missing).

### Frontend — new

- `ui/src/components/ui/command.tsx` — shadcn Command primitive (cmdk wrapper).
- `ui/src/components/LibrarySearchCombobox.tsx` — inline combobox.
- `ui/src/components/CommandPalette.tsx` — global ⌘K dialog.
- `ui/src/components/ShelfDraftProvider.tsx` — Context+Provider exposing `useShelfDraftDialog().open()`, mirroring `UserSettingsDialogProvider`.
- `ui/src/api/search.ts` — typed client for `GET /api/v1/search`.
- `ui/src/hooks/useLogout.ts` — shared mutation + redirect.
- `ui/src/hooks/useDebounce.ts` — generic debounce hook.
- `ui/src/components/__tests__/LibrarySearchCombobox.test.tsx`
- `ui/src/components/__tests__/CommandPalette.test.tsx`
- `e2e/tests/command-palette.spec.ts`

### Frontend — modified

- `ui/src/components/TopBar.tsx` — add `searchVariant?: "input" | "command"` and `commandHint?: boolean` props; render combobox + ⌘K button accordingly.
- `ui/src/components/Sidebar.tsx` — replace local `shelfDraftOpen` `useState` with `useShelfDraftDialog()`. Move `<NewShelfDialog>` JSX out of Sidebar into `ShelfDraftProvider`. Replace inline `logoutMut` with `useLogout()`.
- `ui/src/routes/_app.tsx` — wrap shell with `<ShelfDraftProvider>`; mount `<CommandPalette />`; attach global `keydown` listener for ⌘K; remove the static "⌘K to search" text in StatusBar (the new button replaces it).
- `ui/src/routes/_app.library.tsx` — pass `searchVariant="command"` to `TopBar`.
- `ui/package.json` — add `cmdk` to dependencies.

---

## Task 1: Add cmdk dependency

**Files:**
- Modify: `ui/package.json`

- [ ] **Step 1: Install cmdk via Bun**

```bash
cd ui && bun add cmdk@^1.0.0
```

Expected: `cmdk` appears under `dependencies` in `ui/package.json`; `bun.lock` updates.

- [ ] **Step 2: Verify the install**

```bash
cd ui && bun pm ls 2>/dev/null | grep -i cmdk
```

Expected: prints `cmdk@1.x.x`.

- [ ] **Step 3: Commit**

```bash
git add ui/package.json ui/bun.lock
git commit -m "deps(ui): add cmdk for Command primitive"
```

---

## Task 2: Create shadcn Command primitive

**Files:**
- Create: `ui/src/components/ui/command.tsx`

- [ ] **Step 1: Write the primitive file**

```tsx
"use client"

import * as React from "react"
import { Command as CommandPrimitive } from "cmdk"
import { Search } from "lucide-react"

import { cn } from "@/lib/utils"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

function Command({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive>) {
  return (
    <CommandPrimitive
      data-slot="command"
      className={cn(
        "bg-popover text-popover-foreground flex h-full w-full flex-col overflow-hidden rounded-md",
        className
      )}
      {...props}
    />
  )
}

function CommandDialog({
  title = "Command Palette",
  description = "Search for a command to run...",
  children,
  className,
  showCloseButton = true,
  ...props
}: React.ComponentProps<typeof Dialog> & {
  title?: string
  description?: string
  className?: string
  showCloseButton?: boolean
}) {
  return (
    <Dialog {...props}>
      <DialogHeader className="sr-only">
        <DialogTitle>{title}</DialogTitle>
        <DialogDescription>{description}</DialogDescription>
      </DialogHeader>
      <DialogContent
        className={cn("overflow-hidden p-0", className)}
        showCloseButton={showCloseButton}
      >
        <Command className="[&_[cmdk-group-heading]]:text-muted-foreground **:data-[slot=command-input-wrapper]:h-12 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group]]:px-2 [&_[cmdk-group]:not([hidden])_~[cmdk-group]]:pt-0 [&_[cmdk-input-wrapper]_svg]:h-5 [&_[cmdk-input-wrapper]_svg]:w-5 [&_[cmdk-input]]:h-12 [&_[cmdk-item]]:px-2 [&_[cmdk-item]]:py-3 [&_[cmdk-item]_svg]:h-5 [&_[cmdk-item]_svg]:w-5">
          {children}
        </Command>
      </DialogContent>
    </Dialog>
  )
}

function CommandInput({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Input>) {
  return (
    <div
      data-slot="command-input-wrapper"
      className="flex h-9 items-center gap-2 border-b px-3"
    >
      <Search className="size-4 shrink-0 opacity-50" />
      <CommandPrimitive.Input
        data-slot="command-input"
        className={cn(
          "placeholder:text-muted-foreground flex h-10 w-full rounded-md bg-transparent py-3 text-sm outline-hidden disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        {...props}
      />
    </div>
  )
}

function CommandList({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.List>) {
  return (
    <CommandPrimitive.List
      data-slot="command-list"
      className={cn(
        "max-h-[300px] scroll-py-1 overflow-x-hidden overflow-y-auto",
        className
      )}
      {...props}
    />
  )
}

function CommandEmpty(
  props: React.ComponentProps<typeof CommandPrimitive.Empty>
) {
  return (
    <CommandPrimitive.Empty
      data-slot="command-empty"
      className="py-6 text-center text-sm"
      {...props}
    />
  )
}

function CommandGroup({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Group>) {
  return (
    <CommandPrimitive.Group
      data-slot="command-group"
      className={cn(
        "text-foreground [&_[cmdk-group-heading]]:text-muted-foreground overflow-hidden p-1 [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:font-medium",
        className
      )}
      {...props}
    />
  )
}

function CommandSeparator({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Separator>) {
  return (
    <CommandPrimitive.Separator
      data-slot="command-separator"
      className={cn("bg-border -mx-1 h-px", className)}
      {...props}
    />
  )
}

function CommandItem({
  className,
  ...props
}: React.ComponentProps<typeof CommandPrimitive.Item>) {
  return (
    <CommandPrimitive.Item
      data-slot="command-item"
      className={cn(
        "data-[selected=true]:bg-accent data-[selected=true]:text-accent-foreground [&_svg:not([class*='text-'])]:text-muted-foreground relative flex cursor-default items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-hidden select-none data-[disabled=true]:pointer-events-none data-[disabled=true]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className
      )}
      {...props}
    />
  )
}

function CommandShortcut({
  className,
  ...props
}: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="command-shortcut"
      className={cn(
        "text-muted-foreground ml-auto text-xs tracking-widest",
        className
      )}
      {...props}
    />
  )
}

export {
  Command,
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
}
```

- [ ] **Step 2: Verify it typechecks**

Run: `cd ui && bun run typecheck`
Expected: PASS (no new errors).

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/ui/command.tsx
git commit -m "feat(ui): add shadcn Command primitive"
```

---

## Task 3: Backend — `LibraryRepo.SearchSuggestBooks` and `SearchSuggestLibraries`

Add two slim methods to `internal/repo/library.go`. Books reuse the existing tsv index; libraries do an `ILIKE` since the table is tiny.

**Files:**
- Modify: `internal/repo/library.go` (append at the end of the file, before the helper functions)

- [ ] **Step 1: Add the new model types**

Append to `internal/repo/library.go` (above the new methods):

```go
// SuggestBook is the slim shape returned by SearchSuggestBooks. No progress,
// no locks, no extended metadata — just enough for an autocomplete row.
type SuggestBook struct {
	ID       string
	Title    string
	Author   string
	HasCover bool
}

// SuggestLibrary is the slim shape for library autocomplete rows.
type SuggestLibrary struct {
	ID   string
	Name string
	Slug string
}
```

- [ ] **Step 2: Add `SearchSuggestBooks`**

Append to `internal/repo/library.go`:

```go
// SearchSuggestBooks returns the top `limit` books matching `q` for the
// autocomplete surfaces. Reuses the same tsv FTS column the main book
// listing uses (idx_books_tsv GIN). `limit` is assumed already clamped
// by the caller (service caps at 20).
func (r *LibraryRepo) SearchSuggestBooks(ctx context.Context, q string, limit int) ([]SuggestBook, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT b.id, b.title, b.author, b.has_cover
		FROM books b
		WHERE b.deleted_at IS NULL
		  AND b.tsv @@ websearch_to_tsquery('english', $1)
		ORDER BY ts_rank(b.tsv, websearch_to_tsquery('english', $1)) DESC,
		         b.title ASC
		LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestBook
	for rows.Next() {
		var b SuggestBook
		if err := rows.Scan(&b.ID, &b.Title, &b.Author, &b.HasCover); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Add `SearchSuggestLibraries`**

Append to `internal/repo/library.go`:

```go
// SearchSuggestLibraries returns libraries whose name matches `q`. Today
// every authenticated user can see every library (mirrors GET /libraries),
// so there is no per-user filter here — adopt one if/when library
// visibility becomes user-scoped.
func (r *LibraryRepo) SearchSuggestLibraries(ctx context.Context, q string, limit int) ([]SuggestLibrary, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.name, l.slug
		FROM libraries l
		WHERE l.name ILIKE '%' || $1 || '%'
		ORDER BY l.name ASC
		LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestLibrary
	for rows.Next() {
		var l SuggestLibrary
		if err := rows.Scan(&l.ID, &l.Name, &l.Slug); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Verify the package builds**

Run: `go build ./internal/repo/...`
Expected: PASS, no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/repo/library.go
git commit -m "feat(repo): add slim search suggest methods for books and libraries"
```

---

## Task 4: Backend — `ShelfRepo.SearchSuggest`

**Files:**
- Modify: `internal/repo/shelf.go` (append at the bottom of the file)

- [ ] **Step 1: Add the slim shape and method**

Append to `internal/repo/shelf.go`:

```go
// SuggestShelf is the slim shape returned by SearchSuggest for the
// autocomplete surfaces.
type SuggestShelf struct {
	Slug   string
	Name   string
	Accent string
}

// SearchSuggest returns the user's shelves whose name matches `q`. Used by
// the global command palette; per-user scoping is enforced via the
// user_id WHERE clause.
func (r *ShelfRepo) SearchSuggest(ctx context.Context, userID, q string, limit int) ([]SuggestShelf, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT s.slug, s.name, s.accent
		FROM shelves s
		WHERE s.user_id = $1
		  AND s.name ILIKE '%' || $2 || '%'
		ORDER BY s.name ASC
		LIMIT $3
	`, userID, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SuggestShelf
	for rows.Next() {
		var s SuggestShelf
		if err := rows.Scan(&s.Slug, &s.Name, &s.Accent); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

- [ ] **Step 2: Verify the package builds**

Run: `go build ./internal/repo/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/repo/shelf.go
git commit -m "feat(repo): add SearchSuggest for shelves"
```

---

## Task 5: Backend — `SearchService`

**Files:**
- Create: `internal/service/search.go`

- [ ] **Step 1: Verify `golang.org/x/sync` is available**

Run: `go list -m golang.org/x/sync 2>/dev/null`
Expected: prints a version (likely already pulled in transitively). If empty, run `go get golang.org/x/sync@latest` then continue.

- [ ] **Step 2: Write the service file**

```go
package service

import (
	"context"
	"errors"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/blackforge/embookshelf/internal/repo"
)

// SearchService fans out the three suggest queries that power the command
// palette and the library page combobox. Each result group is independent;
// they run concurrently and an error in any cancels the others.
type SearchService struct {
	lib   *repo.LibraryRepo
	shelf *repo.ShelfRepo
}

// SearchResult is the slim payload returned to the HTTP layer. The handler
// projects each row into its wire DTO; this struct is package-internal
// shape only.
type SearchResult struct {
	Books     []repo.SuggestBook
	Shelves   []repo.SuggestShelf
	Libraries []repo.SuggestLibrary
}

// ErrEmptyQuery is returned when the trimmed query is empty.
var ErrEmptyQuery = errors.New("search: query is required")

const (
	defaultSuggestLimit = 8
	maxSuggestLimit     = 20
)

func NewSearchService(lib *repo.LibraryRepo, shelf *repo.ShelfRepo) *SearchService {
	return &SearchService{lib: lib, shelf: shelf}
}

// Suggest runs the three repo queries in parallel and assembles the
// result. `limit` is clamped to [1, 20]; <=0 falls back to the default.
func (s *SearchService) Suggest(ctx context.Context, userID, q string, limit int) (SearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return SearchResult{}, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = defaultSuggestLimit
	}
	if limit > maxSuggestLimit {
		limit = maxSuggestLimit
	}

	var result SearchResult
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		books, err := s.lib.SearchSuggestBooks(ctx, q, limit)
		if err != nil {
			return err
		}
		result.Books = books
		return nil
	})
	g.Go(func() error {
		shelves, err := s.shelf.SearchSuggest(ctx, userID, q, limit)
		if err != nil {
			return err
		}
		result.Shelves = shelves
		return nil
	})
	g.Go(func() error {
		libs, err := s.lib.SearchSuggestLibraries(ctx, q, limit)
		if err != nil {
			return err
		}
		result.Libraries = libs
		return nil
	})
	if err := g.Wait(); err != nil {
		return SearchResult{}, err
	}
	return result, nil
}
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./internal/service/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/service/search.go
git commit -m "feat(service): add SearchService for command palette suggest"
```

---

## Task 6: Backend — handler + DI wiring + route registration

**Files:**
- Create: `internal/handler/search.go`
- Modify: `internal/handler/handler.go`
- Modify: `internal/handler/router.go`
- Modify: `cmd/embookshelf/main.go`

- [ ] **Step 1: Write the handler**

Create `internal/handler/search.go`:

```go
package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blackforge/embookshelf/internal/repo"
	"github.com/blackforge/embookshelf/internal/service"
)

type suggestBookDTO struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	Cover    string `json:"cover"`
	HasCover bool   `json:"hasCover"`
}

type suggestShelfDTO struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Accent string `json:"accent"`
}

type suggestLibraryDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type searchResponse struct {
	Books     []suggestBookDTO    `json:"books"`
	Shelves   []suggestShelfDTO   `json:"shelves"`
	Libraries []suggestLibraryDTO `json:"libraries"`
}

// Search powers the global command palette and library combobox. Returns
// the top matches across books, shelves (per-user), and libraries.
//
//	?q=<text>       required, trimmed; empty → 400
//	?limit=<int>    optional, default 8, capped at 20 by the service
func (h *Handler) Search(c *gin.Context) {
	userID := requireUserID(c)
	if userID == "" {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		writeError(c, http.StatusBadRequest, "q is required")
		return
	}
	limit := 0 // service applies the default
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeError(c, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = n
	}

	result, err := h.search.Suggest(c.Request.Context(), userID, q, limit)
	if err != nil {
		if errors.Is(err, service.ErrEmptyQuery) {
			writeError(c, http.StatusBadRequest, "q is required")
			return
		}
		writeServerError(c, "search suggest", err)
		return
	}

	c.JSON(http.StatusOK, searchResponse{
		Books:     toSuggestBookDTOs(result.Books),
		Shelves:   toSuggestShelfDTOs(result.Shelves),
		Libraries: toSuggestLibraryDTOs(result.Libraries),
	})
}

func toSuggestBookDTOs(in []repo.SuggestBook) []suggestBookDTO {
	out := make([]suggestBookDTO, 0, len(in))
	for _, b := range in {
		cover := ""
		if b.HasCover {
			cover = "/api/v1/books/" + b.ID + "/cover"
		}
		out = append(out, suggestBookDTO{
			ID:       b.ID,
			Title:    b.Title,
			Author:   b.Author,
			Cover:    cover,
			HasCover: b.HasCover,
		})
	}
	return out
}

func toSuggestShelfDTOs(in []repo.SuggestShelf) []suggestShelfDTO {
	out := make([]suggestShelfDTO, 0, len(in))
	for _, s := range in {
		out = append(out, suggestShelfDTO{Slug: s.Slug, Name: s.Name, Accent: s.Accent})
	}
	return out
}

func toSuggestLibraryDTOs(in []repo.SuggestLibrary) []suggestLibraryDTO {
	out := make([]suggestLibraryDTO, 0, len(in))
	for _, l := range in {
		out = append(out, suggestLibraryDTO{ID: l.ID, Name: l.Name, Slug: l.Slug})
	}
	return out
}
```

- [ ] **Step 2: Add the service field to Handler + Deps**

Edit `internal/handler/handler.go`. In the `Handler` struct add (after `oidc`):

```go
	search       *service.SearchService
```

In the `Deps` struct add (after `OIDC`):

```go
	Search       *service.SearchService
```

In `New(d Deps)` extend the literal so it now reads (showing the new line, leave the rest in place):

```go
	return &Handler{
		cfg: d.Cfg, static: d.Static,
		lib: d.Lib, shelf: d.Shelf, auth: d.Auth,
		bookdrop: d.BookDrop, progress: d.Progress, enrich: d.Enrich,
		annotations: d.Annotations, stats: d.Stats,
		readingStats: d.ReadingStats,
		devices:      d.Devices,
		oidc:         d.OIDC,
		search:       d.Search,
		appSettings:  d.AppSettings,
		covers:       d.Covers,
		hub:          d.Hub, queue: d.Queue,
	}
```

- [ ] **Step 3: Register the route**

Edit `internal/handler/router.go`. Inside the `authed := api.Group("")` block, add (next to the other top-level book routes, e.g. just after `authed.GET("/instance", h.InstanceSummary)`):

```go
				// Cross-entity search powering the global command palette
				// and the library page combobox.
				authed.GET("/search", h.Search)
```

- [ ] **Step 4: Wire DI in main**

Edit `cmd/embookshelf/main.go`. After the existing `shelfSvc := service.NewShelfService(shelfRepo)` line, add:

```go
	searchSvc := service.NewSearchService(libRepo, shelfRepo)
```

Then, in the `handler.New(handler.Deps{...})` call near the bottom of `main`, pass it. The exact field name to add to the literal:

```go
		Search:       searchSvc,
```

(Keep the field aligned with the others; the order doesn't matter for struct literals with field names.)

- [ ] **Step 5: Verify the binary builds**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Smoke-test the endpoint manually**

Run: `make db-up && make seed && make dev` in one terminal, then in another:

```bash
curl -s -b /tmp/cookies.txt -c /tmp/cookies.txt \
  -X POST http://localhost:6060/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@local","password":"changeme"}' >/dev/null
curl -s -b /tmp/cookies.txt 'http://localhost:6060/api/v1/search?q=a&limit=3' | jq .
```

Expected: JSON with `books`, `shelves`, `libraries` keys (arrays, possibly empty depending on seed).

Stop the dev server (Ctrl-C in the first terminal) when done.

- [ ] **Step 7: Commit**

```bash
git add internal/handler/search.go internal/handler/handler.go internal/handler/router.go cmd/embookshelf/main.go go.mod go.sum
git commit -m "feat(api): add GET /api/v1/search endpoint for command palette"
```

(Include `go.mod`/`go.sum` only if the `golang.org/x/sync` step in Task 5 actually modified them.)

---

## Task 7: Frontend — `searchSuggest` API client

**Files:**
- Create: `ui/src/api/search.ts`

- [ ] **Step 1: Write the client**

```ts
import { api } from "./client"

// Mirrors internal/handler/search.go suggestBookDTO. `cover` is "" when
// the book has no cover; consumers should fall back to a placeholder.
export type SuggestBook = {
  id: string
  title: string
  author: string
  cover: string
  hasCover: boolean
}

export type SuggestShelf = {
  slug: string
  name: string
  accent: string
}

export type SuggestLibrary = {
  id: string
  name: string
  slug: string
}

export type SearchSuggestResult = {
  books: Array<SuggestBook>
  shelves: Array<SuggestShelf>
  libraries: Array<SuggestLibrary>
}

// searchQueryKey is the shared TanStack Query key. Same key is used by
// the inline combobox and the global palette so two surfaces with the
// same input share one network call.
export function searchQueryKey(q: string, limit = 8) {
  return ["search", q, limit] as const
}

export async function searchSuggest(
  q: string,
  limit = 8
): Promise<SearchSuggestResult> {
  const params = new URLSearchParams({ q })
  if (limit !== 8) params.set("limit", String(limit))
  return api<SearchSuggestResult>(`/api/v1/search?${params.toString()}`)
}
```

- [ ] **Step 2: Verify it typechecks**

Run: `cd ui && bun run typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add ui/src/api/search.ts
git commit -m "feat(ui): add searchSuggest API client"
```

---

## Task 8: Frontend — `useDebounce` hook

**Files:**
- Create: `ui/src/hooks/useDebounce.ts`

- [ ] **Step 1: Write the hook**

```ts
import { useEffect, useState } from "react"

/**
 * useDebounce returns `value` after it has been stable for `delayMs`.
 * Used by the search surfaces to throttle keystrokes into one HTTP
 * request per pause.
 */
export function useDebounce<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(t)
  }, [value, delayMs])
  return debounced
}
```

- [ ] **Step 2: Verify**

Run: `cd ui && bun run typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add ui/src/hooks/useDebounce.ts
git commit -m "feat(ui): add useDebounce hook"
```

---

## Task 9: Frontend — `useLogout` hook

Extract the logout mutation from `Sidebar.tsx` so `CommandPalette` can reuse the same redirect + cache invalidation.

**Files:**
- Create: `ui/src/hooks/useLogout.ts`
- Modify: `ui/src/components/Sidebar.tsx`

- [ ] **Step 1: Write the hook**

Create `ui/src/hooks/useLogout.ts`:

```ts
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { logout as apiLogout, meQueryKey } from "@/api/auth"

/**
 * useLogout wraps the logout API call with the standard side effects:
 * clear the cached `me` query and redirect to /login. Used by both the
 * sidebar user badge and the command palette so they stay in lock-step.
 */
export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: apiLogout,
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null)
      void navigate({ to: "/login", replace: true })
    },
  })
}
```

- [ ] **Step 2: Replace inline mutation in Sidebar**

In `ui/src/components/Sidebar.tsx`, find the existing block (around lines 101–107):

```ts
  const logoutMut = useMutation({
    mutationFn: apiLogout,
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null)
      void navigate({ to: "/login", replace: true })
    },
  })
```

Replace with:

```ts
  const logoutMut = useLogout()
```

Then update the imports at the top of `Sidebar.tsx`:
- Remove `apiLogout` from the `@/api/auth` import (keep `fetchMe`, `meQueryKey`).
- If `useNavigate` and `useMutation` / `useQueryClient` are no longer used elsewhere in this file, remove them too. Run typecheck (next step) to catch this.
- Add: `import { useLogout } from "@/hooks/useLogout"`.

- [ ] **Step 3: Verify nothing broke**

Run: `cd ui && bun run typecheck && bun run lint`
Expected: PASS. If lint complains about unused imports, prune them.

- [ ] **Step 4: Commit**

```bash
git add ui/src/hooks/useLogout.ts ui/src/components/Sidebar.tsx
git commit -m "refactor(ui): extract useLogout hook shared between Sidebar and palette"
```

---

## Task 10: Frontend — `ShelfDraftProvider` (lift create-shelf dialog)

Mirror the existing `UserSettingsDialogProvider` pattern. Move the `<NewShelfDialog>` JSX (currently inside Sidebar around lines 340–355) into a new provider so the command palette can call `open()`.

**Files:**
- Create: `ui/src/components/ShelfDraftProvider.tsx`
- Modify: `ui/src/components/Sidebar.tsx`
- Modify: `ui/src/routes/_app.tsx`

- [ ] **Step 1: Read the relevant Sidebar section**

Run:

```bash
grep -n "NewShelfDialog\|shelfDraftOpen\|createShelfMut" /Users/shbodya/Documents/blackforge/embookshelf/ui/src/components/Sidebar.tsx
```

You'll see:
- `createShelfMut` mutation at ~line 112
- `shelfDraftOpen` state at ~line 156
- `setShelfDraftOpen(true)` button click at ~line 262
- `<NewShelfDialog>` render at ~line 340

The provider absorbs all four — Sidebar keeps only the open button, calling `useShelfDraftDialog().open()`.

- [ ] **Step 2: Write the provider**

Create `ui/src/components/ShelfDraftProvider.tsx`:

```tsx
import { createContext, useCallback, useContext, useMemo, useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import type { ReactNode } from "react"

import type { ApiError } from "@/api/client"
import { createShelf, shelvesQueryKey } from "@/api/books"
import type { ShelfAccent } from "./AccentPicker"
import { NewShelfDialog } from "./NewShelfDialog"

type ShelfDraftDialogContextValue = {
  open: () => void
}

const ShelfDraftDialogContext =
  createContext<ShelfDraftDialogContextValue | null>(null)

export function useShelfDraftDialog(): ShelfDraftDialogContextValue {
  const ctx = useContext(ShelfDraftDialogContext)
  if (!ctx) {
    throw new Error(
      "useShelfDraftDialog must be used inside <ShelfDraftProvider>"
    )
  }
  return ctx
}

/**
 * Hosts the "create a regular shelf" dialog and exposes `open()` via
 * context. Mirrors UserSettingsDialogProvider so the sidebar header
 * button and the command palette can both trigger it.
 */
export function ShelfDraftProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [isOpen, setOpen] = useState(false)

  const createShelfMut = useMutation({
    mutationFn: (args: { name: string; accent: ShelfAccent }) =>
      createShelf(args.name, args.accent),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      setOpen(false)
    },
  })

  const value = useMemo<ShelfDraftDialogContextValue>(
    () => ({ open: () => setOpen(true) }),
    []
  )

  return (
    <ShelfDraftDialogContext.Provider value={value}>
      {children}
      <NewShelfDialog
        open={isOpen}
        onOpenChange={(open) => {
          if (!open) createShelfMut.reset()
          setOpen(open)
        }}
        busy={createShelfMut.isPending}
        error={(createShelfMut.error as ApiError | null)?.message ?? null}
        onSubmit={(draft) => createShelfMut.mutate(draft)}
      />
    </ShelfDraftDialogContext.Provider>
  )
}
```

> **Note:** The exact prop names on `<NewShelfDialog>` (`busy`, `error`, `onSubmit`, `onOpenChange`) and the import path are taken from the current Sidebar.tsx call site — verify by reading the existing render before writing this file. If `NewShelfDialog` is currently a local component inside `Sidebar.tsx`, extract it to its own file `ui/src/components/NewShelfDialog.tsx` first (move the function + its types verbatim), and import from there in both Sidebar and this provider.

- [ ] **Step 3: Update Sidebar to use the hook**

In `ui/src/components/Sidebar.tsx`:

1. Remove the `createShelfMut = useMutation({ ... mutationFn: createShelf ... })` block (around lines 112–119).
2. Remove `const [shelfDraftOpen, setShelfDraftOpen] = useState(false)` (around line 156).
3. Remove the `<NewShelfDialog ...>` render at the bottom of the component (around lines 340–355).
4. Replace the `setShelfDraftOpen(true)` click handler with a call to the hook. At the top of `AppSidebar()` add:

```ts
  const shelfDraft = useShelfDraftDialog()
```

And change the click handler from `onClick={() => setShelfDraftOpen(true)}` to `onClick={() => shelfDraft.open()}`. The `disabled={createShelfMut.isPending}` line is no longer relevant since the mutation lives in the provider — drop it (the dialog itself is gated while busy).

5. Update imports: remove `createShelf` from `@/api/books`; add `import { useShelfDraftDialog } from "@/components/ShelfDraftProvider"`. Remove `useState` if no longer used.

- [ ] **Step 4: Mount the provider in `_app.tsx`**

Edit `ui/src/routes/_app.tsx`. Wrap the existing tree (inside `UserSettingsDialogProvider`):

```tsx
import { ShelfDraftProvider } from "@/components/ShelfDraftProvider"

// ... inside AppLayout(), keep the existing TooltipProvider /
// UserSettingsDialogProvider wrappers and add ShelfDraftProvider:

  return (
    <TooltipProvider delayDuration={100}>
      <UserSettingsDialogProvider>
        <ShelfDraftProvider>
          <SidebarProvider className="h-screen overflow-hidden">
            <AppSidebar />
            <SidebarInset className="min-h-0 overflow-hidden">
              <div className="main-content">
                <Outlet />
              </div>
              <StatusBar />
            </SidebarInset>
          </SidebarProvider>
        </ShelfDraftProvider>
      </UserSettingsDialogProvider>
    </TooltipProvider>
  )
```

- [ ] **Step 5: Verify**

Run: `cd ui && bun run typecheck && bun run lint`
Expected: PASS.

Manual smoke: `make up`, open http://localhost:5173/library, click the "+" next to Shelves in the sidebar — the dialog should still open and accept submissions.

- [ ] **Step 6: Commit**

```bash
git add ui/src/components/ShelfDraftProvider.tsx ui/src/components/NewShelfDialog.tsx ui/src/components/Sidebar.tsx ui/src/routes/_app.tsx
git commit -m "refactor(ui): lift create-shelf dialog into ShelfDraftProvider"
```

(Include `NewShelfDialog.tsx` only if Step 2 required extracting it from Sidebar.)

---

## Task 11: Frontend — `LibrarySearchCombobox`

**Files:**
- Create: `ui/src/components/LibrarySearchCombobox.tsx`

- [ ] **Step 1: Write the component**

```tsx
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Popover,
  PopoverAnchor,
  PopoverContent,
} from "@/components/ui/popover"
import { useDebounce } from "@/hooks/useDebounce"
import { searchQueryKey, searchSuggest } from "@/api/search"

type Props = {
  value: string
  onSearchChange: (next: string) => void
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 200

export function LibrarySearchCombobox({ value, onSearchChange }: Props) {
  const navigate = useNavigate()
  const [focused, setFocused] = useState(false)
  const debounced = useDebounce(value, DEBOUNCE_MS)
  const enabled = debounced.trim().length >= MIN_QUERY_LENGTH

  const query = useQuery({
    queryKey: searchQueryKey(debounced, 8),
    queryFn: () => searchSuggest(debounced, 8),
    enabled,
    staleTime: 30_000,
  })

  const open = focused && enabled
  const books = query.data?.books ?? []

  return (
    <Popover open={open} onOpenChange={() => { /* anchored, controlled by focus */ }}>
      <PopoverAnchor asChild>
        <div style={{ position: "relative", width: 280 }}>
          <Command shouldFilter={false} className="rounded-md border">
            <CommandInput
              placeholder="Search library…"
              value={value}
              onValueChange={onSearchChange}
              onFocus={() => setFocused(true)}
              onBlur={() => {
                // Delay so click on a CommandItem fires before close.
                setTimeout(() => setFocused(false), 150)
              }}
            />
          </Command>
        </div>
      </PopoverAnchor>
      <PopoverContent
        align="start"
        sideOffset={4}
        className="w-[280px] p-0"
        onOpenAutoFocus={(e) => e.preventDefault()}
      >
        <Command shouldFilter={false}>
          <CommandList>
            {query.isLoading ? (
              <div className="px-3 py-4 text-xs text-(--color-ink-3)">
                Searching…
              </div>
            ) : books.length === 0 ? (
              <CommandEmpty>No matches</CommandEmpty>
            ) : (
              <CommandGroup heading="Books">
                {books.map((b) => (
                  <CommandItem
                    key={b.id}
                    value={`${b.id} ${b.title} ${b.author}`}
                    onSelect={() => {
                      void navigate({ to: "/book/$id", params: { id: b.id } })
                    }}
                  >
                    {b.cover ? (
                      <img
                        src={b.cover}
                        alt=""
                        width={24}
                        height={32}
                        style={{ objectFit: "cover", borderRadius: 2 }}
                      />
                    ) : (
                      <div
                        style={{
                          width: 24,
                          height: 32,
                          borderRadius: 2,
                          background: "var(--color-rule-soft)",
                        }}
                      />
                    )}
                    <div className="flex min-w-0 flex-col">
                      <span className="truncate">{b.title}</span>
                      <span className="text-xs text-(--color-ink-3)">{b.author}</span>
                    </div>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
```

> **Note:** `Icon` import is left in case the placeholder block is later replaced with the existing `Icon name="book"` icon — keep or drop based on lint feedback.

- [ ] **Step 2: Verify it typechecks**

Run: `cd ui && bun run typecheck`
Expected: PASS. If `useNavigate` typing complains about the `/book/$id` route, run `bun run dev` once to regenerate `routeTree.gen.ts`, then re-run typecheck.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/LibrarySearchCombobox.tsx
git commit -m "feat(ui): add LibrarySearchCombobox for inline book autocomplete"
```

---

## Task 12: Frontend — `CommandPalette`

**Files:**
- Create: `ui/src/components/CommandPalette.tsx`

- [ ] **Step 1: Write the component**

```tsx
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { fetchMe, meQueryKey } from "@/api/auth"
import { searchQueryKey, searchSuggest } from "@/api/search"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { useSidebar } from "@/components/ui/sidebar"
import { useShelfDraftDialog } from "@/components/ShelfDraftProvider"
import { useUserSettingsDialog } from "@/components/UserSettingsDialog"
import { useDebounce } from "@/hooks/useDebounce"
import { useLogout } from "@/hooks/useLogout"

type Props = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const MIN_QUERY_LENGTH = 2
const DEBOUNCE_MS = 200

export function CommandPalette({ open, onOpenChange }: Props) {
  const navigate = useNavigate()
  const me = useQuery({
    queryKey: meQueryKey,
    queryFn: fetchMe,
    staleTime: 60_000,
  })
  const isAdmin = me.data?.role === "admin"

  const shelfDraft = useShelfDraftDialog()
  const userSettings = useUserSettingsDialog()
  const sidebar = useSidebar()
  const logoutMut = useLogout()

  const [value, setValue] = useState("")
  const debounced = useDebounce(value, DEBOUNCE_MS)
  const enabled = debounced.trim().length >= MIN_QUERY_LENGTH

  const query = useQuery({
    queryKey: searchQueryKey(debounced, 8),
    queryFn: () => searchSuggest(debounced, 8),
    enabled: open && enabled,
    staleTime: 30_000,
  })

  function close() {
    onOpenChange(false)
    setValue("")
  }

  function run(handler: () => void) {
    handler()
    close()
  }

  const data = query.data
  const hasSearchResults =
    enabled &&
    !!data &&
    (data.books.length > 0 ||
      data.shelves.length > 0 ||
      data.libraries.length > 0)

  return (
    <CommandDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Command palette"
      description="Search the library or run a command."
      className="sm:max-w-[640px]"
    >
      <CommandInput
        placeholder="Search books, shelves, or run a command…"
        value={value}
        onValueChange={setValue}
        autoFocus
      />
      <CommandList>
        {enabled && !query.isLoading && !hasSearchResults && (
          <CommandEmpty>No matches</CommandEmpty>
        )}

        <CommandGroup heading="Quick actions">
          <CommandItem
            value="open bookdrop intake upload"
            onSelect={() => run(() => void navigate({ to: "/bookdrop" }))}
          >
            Open Bookdrop intake
          </CommandItem>
          <CommandItem
            value="new shelf create collection"
            onSelect={() => run(() => shelfDraft.open())}
          >
            New shelf
          </CommandItem>
          <CommandItem
            value="open user settings preferences account"
            onSelect={() => run(() => userSettings.open())}
          >
            Open user settings
          </CommandItem>
          <CommandItem
            value="toggle sidebar collapse expand"
            onSelect={() => run(() => sidebar.toggleSidebar())}
          >
            Toggle sidebar
          </CommandItem>
          {isAdmin && (
            <CommandItem
              value="library scan rescan reindex admin"
              onSelect={() => run(() => void navigate({ to: "/settings" }))}
            >
              Library scan (Settings → Libraries)
            </CommandItem>
          )}
          <CommandItem
            value="sign out logout"
            onSelect={() => run(() => logoutMut.mutate())}
          >
            Sign out
          </CommandItem>
        </CommandGroup>

        <CommandSeparator />

        <CommandGroup heading="Navigation">
          <CommandItem
            value="library all books"
            onSelect={() => run(() => void navigate({ to: "/library" }))}
          >
            Library
          </CommandItem>
          <CommandItem
            value="bookdrop"
            onSelect={() => run(() => void navigate({ to: "/bookdrop" }))}
          >
            Bookdrop
          </CommandItem>
          <CommandItem
            value="notebook annotations highlights"
            onSelect={() => run(() => void navigate({ to: "/notebook" }))}
          >
            Notebook
          </CommandItem>
          <CommandItem
            value="stats reading"
            onSelect={() => run(() => void navigate({ to: "/stats" }))}
          >
            Stats
          </CommandItem>
          <CommandItem
            value="settings"
            onSelect={() => run(() => void navigate({ to: "/settings" }))}
          >
            Settings
          </CommandItem>
        </CommandGroup>

        {enabled && data && data.books.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Books">
              {data.books.map((b) => (
                <CommandItem
                  key={b.id}
                  value={`book ${b.id} ${b.title} ${b.author}`}
                  onSelect={() =>
                    run(() => void navigate({ to: "/book/$id", params: { id: b.id } }))
                  }
                >
                  <span>{b.title}</span>
                  <span className="ml-auto text-xs text-(--color-ink-3)">
                    {b.author}
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {enabled && data && data.shelves.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Shelves">
              {data.shelves.map((s) => (
                <CommandItem
                  key={s.slug}
                  value={`shelf ${s.slug} ${s.name}`}
                  onSelect={() =>
                    run(() =>
                      void navigate({
                        to: "/library",
                        search: { shelf: s.slug },
                      })
                    )
                  }
                >
                  {s.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {enabled && data && data.libraries.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Libraries">
              {data.libraries.map((l) => (
                <CommandItem
                  key={l.id}
                  value={`library ${l.id} ${l.name}`}
                  onSelect={() =>
                    run(() =>
                      void navigate({
                        to: "/library",
                        search: { library: l.slug },
                      })
                    )
                  }
                >
                  {l.name}
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}
      </CommandList>
    </CommandDialog>
  )
}
```

- [ ] **Step 2: Verify it typechecks**

Run: `cd ui && bun run typecheck`
Expected: PASS. If route param types complain, regenerate the route tree by running `bun run dev` once, then re-run typecheck.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/CommandPalette.tsx
git commit -m "feat(ui): add global CommandPalette with books, shelves, libraries, actions"
```

---

## Task 13: Frontend — TopBar `searchVariant` + `commandHint`

**Files:**
- Modify: `ui/src/components/TopBar.tsx`

- [ ] **Step 1: Edit the props and render**

Replace the entire `ui/src/components/TopBar.tsx` body with:

```tsx
import { Fragment } from "react"
import type { ReactNode } from "react"

import { Icon } from "./Icon"
import { Input } from "@/components/ui/input"
import { LibrarySearchCombobox } from "./LibrarySearchCombobox"

type SearchVariant = "input" | "command"

type TopBarProps = {
  title: ReactNode
  subtitle?: ReactNode
  search?: string
  setSearch?: (value: string) => void
  searchVariant?: SearchVariant
  commandHint?: boolean
  right?: ReactNode
  crumbs?: Array<string>
}

// Top bar — sticky header above each main view. Matches the prototype's
// padding + sticky behavior so sidebar scroll and crumb layout line up.
export function TopBar({
  title,
  subtitle,
  search,
  setSearch,
  searchVariant = "input",
  commandHint = true,
  right,
  crumbs,
}: TopBarProps) {
  return (
    <div
      style={{
        padding: "18px 32px 14px",
        borderBottom: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-1)",
        position: "sticky",
        top: 0,
        zIndex: 10,
      }}
    >
      {crumbs && crumbs.length > 0 && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            marginBottom: 8,
          }}
        >
          {crumbs.map((c, i) => (
            <Fragment key={`${i}-${c}`}>
              {i > 0 && (
                <Icon name="chevron-right" size={12} className="mono" />
              )}
              <span
                className="t-micro"
                style={{
                  color:
                    i === crumbs.length - 1
                      ? "var(--color-ink-2)"
                      : "var(--color-ink-3)",
                }}
              >
                {c}
              </span>
            </Fragment>
          ))}
        </div>
      )}
      <div style={{ display: "flex", alignItems: "flex-end", gap: 24 }}>
        <div className="grow">
          <h1 className="t-h1" style={{ fontWeight: 500 }}>
            {title}
          </h1>
          {subtitle && (
            <div
              style={{
                color: "var(--color-ink-3)",
                fontSize: 14,
                marginTop: 4,
                fontStyle: "italic",
              }}
            >
              {subtitle}
            </div>
          )}
        </div>

        {setSearch && searchVariant === "command" && (
          <LibrarySearchCombobox
            value={search ?? ""}
            onSearchChange={setSearch}
          />
        )}

        {setSearch && searchVariant === "input" && (
          <div style={{ position: "relative", width: 280 }}>
            <Input
              placeholder="Search library…"
              value={search ?? ""}
              onChange={(e) => setSearch(e.target.value)}
              className="pl-8"
            />
            <div
              style={{
                position: "absolute",
                left: 10,
                top: "50%",
                transform: "translateY(-50%)",
                color: "var(--color-ink-3)",
                pointerEvents: "none",
              }}
            >
              <Icon name="search" size={14} />
            </div>
          </div>
        )}

        {commandHint && (
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(new CustomEvent("embookshelf:open-command"))
            }
            className="inline-flex h-9 items-center gap-2 rounded-md border px-3 text-xs text-(--color-ink-3) hover:text-(--color-ink-1)"
            aria-label="Open command palette"
          >
            <Icon name="search" size={12} />
            <span>Search</span>
            <kbd className="rounded bg-(--color-paper-2) px-1.5 py-0.5 font-mono text-[10px]">
              ⌘K
            </kbd>
          </button>
        )}

        {right}
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Verify it typechecks**

Run: `cd ui && bun run typecheck`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/TopBar.tsx
git commit -m "feat(ui): TopBar searchVariant + ⌘K hint button"
```

---

## Task 14: Frontend — Mount palette + ⌘K listener in `_app.tsx`

**Files:**
- Modify: `ui/src/routes/_app.tsx`

- [ ] **Step 1: Mount and wire**

Edit `ui/src/routes/_app.tsx`. Add the imports at the top:

```tsx
import { useEffect, useState } from "react"

import { CommandPalette } from "@/components/CommandPalette"
```

Replace `AppLayout()` so the palette is mounted at the shell level and ⌘K + the custom event both toggle it. Also drop the literal "⌘K to search" span in `StatusBar` since the TopBar button is now the discoverable affordance.

```tsx
function AppLayout() {
  // Only runs inside the authed shell — unauth'd visitors hit the
  // beforeLoad redirect above and never mount this component, so the
  // EventSource never fires without a valid session cookie.
  useRealtime()

  const [paletteOpen, setPaletteOpen] = useState(false)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setPaletteOpen((prev) => !prev)
      }
    }
    function onCustom() {
      setPaletteOpen(true)
    }
    window.addEventListener("keydown", onKey)
    window.addEventListener("embookshelf:open-command", onCustom)
    return () => {
      window.removeEventListener("keydown", onKey)
      window.removeEventListener("embookshelf:open-command", onCustom)
    }
  }, [])

  return (
    <TooltipProvider delayDuration={100}>
      <UserSettingsDialogProvider>
        <ShelfDraftProvider>
          <SidebarProvider className="h-screen overflow-hidden">
            <AppSidebar />
            <SidebarInset className="min-h-0 overflow-hidden">
              <div className="main-content">
                <Outlet />
              </div>
              <StatusBar />
            </SidebarInset>
          </SidebarProvider>
          <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
        </ShelfDraftProvider>
      </UserSettingsDialogProvider>
    </TooltipProvider>
  )
}
```

In `StatusBar()`, remove the trailing block:

```tsx
      <span>·</span>
      <span>⌘K to search</span>
```

(Leave the rest of StatusBar untouched.)

- [ ] **Step 2: Verify it typechecks and lints**

Run: `cd ui && bun run typecheck && bun run lint`
Expected: PASS.

- [ ] **Step 3: Manual smoke test**

Run: `make up`. In the browser at http://localhost:5173:

- Press ⌘K (Cmd-K on macOS, Ctrl-K elsewhere): the palette opens.
- Press Esc: it closes.
- Type "settings", press Enter: navigates to /settings.
- Open palette, type "new shelf", Enter: shelf draft dialog opens.
- Open palette, type "sign out", Enter: redirected to /login.

If any of these fail, debug before committing. Stop the dev stack when done.

- [ ] **Step 4: Commit**

```bash
git add ui/src/routes/_app.tsx
git commit -m "feat(ui): mount CommandPalette and wire ⌘K shortcut at app shell"
```

---

## Task 15: Frontend — wire library route to use the command variant

**Files:**
- Modify: `ui/src/routes/_app.library.tsx`

- [ ] **Step 1: Add the prop**

In `ui/src/routes/_app.library.tsx`, find the existing `<TopBar ... />` render (around lines 150–160) and add `searchVariant="command"`:

```tsx
      <TopBar
        title="Library"
        // ... existing props ...
        search={search}
        setSearch={setSearch}
        searchVariant="command"
      />
```

- [ ] **Step 2: Manual smoke test**

Run: `make up`. Navigate to http://localhost:5173/library:

- Type "the": the grid filter still applies (current behavior).
- Wait ~200ms: a popover appears under the input listing top book matches.
- Click a suggestion: navigates to that book's detail page.
- Clear the input: popover closes.

- [ ] **Step 3: Commit**

```bash
git add ui/src/routes/_app.library.tsx
git commit -m "feat(ui): use Command-powered combobox on library page"
```

---

## Task 16: Frontend — Vitest tests

**Files:**
- Create: `ui/src/components/__tests__/LibrarySearchCombobox.test.tsx`
- Create: `ui/src/components/__tests__/CommandPalette.test.tsx`

- [ ] **Step 1: Write the combobox test**

Create `ui/src/components/__tests__/LibrarySearchCombobox.test.tsx`:

```tsx
import type { ReactElement } from "react"
import { render, screen, waitFor, fireEvent } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach } from "vitest"

import { LibrarySearchCombobox } from "../LibrarySearchCombobox"

const navigateMock = vi.fn()
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

const searchSuggestMock = vi.fn()
vi.mock("@/api/search", () => ({
  searchQueryKey: (q: string, limit: number) => ["search", q, limit] as const,
  searchSuggest: (q: string, limit: number) => searchSuggestMock(q, limit),
}))

function renderWithClient(ui: ReactElement) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>{ui}</QueryClientProvider>
  )
}

describe("LibrarySearchCombobox", () => {
  beforeEach(() => {
    navigateMock.mockReset()
    searchSuggestMock.mockReset()
  })

  it("calls onSearchChange synchronously while typing (grid filter)", () => {
    const setSearch = vi.fn()
    renderWithClient(
      <LibrarySearchCombobox value="" onSearchChange={setSearch} />
    )
    const input = screen.getByPlaceholderText(/search library/i)
    fireEvent.change(input, { target: { value: "dune" } })
    expect(setSearch).toHaveBeenCalledWith("dune")
  })

  it("renders a book suggestion and navigates on select", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [
        { id: "b1", title: "Dune", author: "Frank Herbert", cover: "", hasCover: false },
      ],
      shelves: [],
      libraries: [],
    })

    function Harness() {
      return <LibrarySearchCombobox value="dune" onSearchChange={() => {}} />
    }
    renderWithClient(<Harness />)
    const input = screen.getByPlaceholderText(/search library/i)
    fireEvent.focus(input)

    await waitFor(() => {
      expect(screen.getByText("Dune")).toBeInTheDocument()
    })

    fireEvent.click(screen.getByText("Dune"))
    expect(navigateMock).toHaveBeenCalledWith({
      to: "/book/$id",
      params: { id: "b1" },
    })
  })

  it("renders 'No matches' when the query returns empty", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [],
      shelves: [],
      libraries: [],
    })
    renderWithClient(
      <LibrarySearchCombobox value="zzzzzz" onSearchChange={() => {}} />
    )
    fireEvent.focus(screen.getByPlaceholderText(/search library/i))
    await waitFor(() => {
      expect(screen.getByText(/no matches/i)).toBeInTheDocument()
    })
  })
})
```

- [ ] **Step 2: Write the palette test**

Create `ui/src/components/__tests__/CommandPalette.test.tsx`:

```tsx
import { render, screen, waitFor, fireEvent } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach } from "vitest"

import { CommandPalette } from "../CommandPalette"

const navigateMock = vi.fn()
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}))

const searchSuggestMock = vi.fn()
vi.mock("@/api/search", () => ({
  searchQueryKey: (q: string, limit: number) => ["search", q, limit] as const,
  searchSuggest: (q: string, limit: number) => searchSuggestMock(q, limit),
}))

const fetchMeMock = vi.fn()
vi.mock("@/api/auth", async () => {
  const actual = await vi.importActual<typeof import("@/api/auth")>(
    "@/api/auth"
  )
  return { ...actual, fetchMe: () => fetchMeMock() }
})

const shelfDraftOpen = vi.fn()
vi.mock("@/components/ShelfDraftProvider", () => ({
  useShelfDraftDialog: () => ({ open: shelfDraftOpen }),
}))

const userSettingsOpen = vi.fn()
vi.mock("@/components/UserSettingsDialog", () => ({
  useUserSettingsDialog: () => ({ open: userSettingsOpen }),
}))

const toggleSidebar = vi.fn()
vi.mock("@/components/ui/sidebar", () => ({
  useSidebar: () => ({ toggleSidebar }),
}))

const logoutMutate = vi.fn()
vi.mock("@/hooks/useLogout", () => ({
  useLogout: () => ({ mutate: logoutMutate }),
}))

function renderPalette(open = true, role: "admin" | "user" = "user") {
  fetchMeMock.mockResolvedValue({
    id: "u1",
    email: "u@local",
    name: "U",
    role,
    display: "U",
    initials: "U",
    createdAt: "",
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <CommandPalette open={open} onOpenChange={() => {}} />
    </QueryClientProvider>
  )
}

describe("CommandPalette", () => {
  beforeEach(() => {
    navigateMock.mockReset()
    searchSuggestMock.mockReset()
    fetchMeMock.mockReset()
    shelfDraftOpen.mockReset()
    userSettingsOpen.mockReset()
    toggleSidebar.mockReset()
    logoutMutate.mockReset()
  })

  it("renders quick actions and navigation with empty input", async () => {
    renderPalette()
    expect(await screen.findByText("Open Bookdrop intake")).toBeInTheDocument()
    expect(screen.getByText("New shelf")).toBeInTheDocument()
    expect(screen.getByText("Library")).toBeInTheDocument()
    expect(screen.getByText("Settings")).toBeInTheDocument()
  })

  it("hides Library scan for non-admin users", async () => {
    renderPalette(true, "user")
    await screen.findByText("Open Bookdrop intake")
    expect(screen.queryByText(/library scan/i)).not.toBeInTheDocument()
  })

  it("shows Library scan for admin users", async () => {
    renderPalette(true, "admin")
    expect(await screen.findByText(/library scan/i)).toBeInTheDocument()
  })

  it("invokes the New shelf action", async () => {
    renderPalette()
    fireEvent.click(await screen.findByText("New shelf"))
    expect(shelfDraftOpen).toHaveBeenCalled()
  })

  it("renders book results after typing", async () => {
    searchSuggestMock.mockResolvedValueOnce({
      books: [
        { id: "b1", title: "Dune", author: "Herbert", cover: "", hasCover: false },
      ],
      shelves: [],
      libraries: [],
    })
    renderPalette()
    const input = await screen.findByPlaceholderText(
      /search books, shelves/i
    )
    fireEvent.change(input, { target: { value: "dune" } })
    await waitFor(() => {
      expect(screen.getByText("Dune")).toBeInTheDocument()
    })
  })
})
```

- [ ] **Step 3: Run the tests**

Run: `cd ui && bun run test`
Expected: PASS for all new tests. If any test fails because of cmdk's `Dialog` portal mounting, wrap the assertions in `await waitFor(...)` calls — cmdk renders inside a Radix portal which mounts asynchronously.

- [ ] **Step 4: Commit**

```bash
git add ui/src/components/__tests__/LibrarySearchCombobox.test.tsx ui/src/components/__tests__/CommandPalette.test.tsx
git commit -m "test(ui): cover LibrarySearchCombobox and CommandPalette"
```

---

## Task 17: E2E — Playwright spec

**Files:**
- Create: `e2e/tests/command-palette.spec.ts`

- [ ] **Step 1: Identify a stable seeded book title**

Run:

```bash
grep -E "INSERT INTO books" /Users/shbodya/Documents/blackforge/embookshelf/scripts/seed.sql | head -3
```

Pick the first book title (e.g. `'Foundation'`) and substitute it for `<SEED_TITLE>` below.

- [ ] **Step 2: Write the spec**

Create `e2e/tests/command-palette.spec.ts`:

```ts
import { test, expect } from "@playwright/test"

const SEED_TITLE = "<SEED_TITLE>" // see Task 17 Step 1

test.describe("command palette", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/login")
    await page.getByLabel(/email/i).fill("admin@local")
    await page.getByLabel(/password/i).fill("changeme")
    await page.getByRole("button", { name: /sign in/i }).click()
    await page.waitForURL("/")
  })

  test("⌘K opens palette and navigates to a book", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+K")
    const input = page.getByPlaceholder(/search books, shelves/i)
    await expect(input).toBeVisible()
    await input.fill(SEED_TITLE)
    await page.getByRole("option", { name: new RegExp(SEED_TITLE, "i") }).first().click()
    await expect(page).toHaveURL(/\/book\//)
  })

  test("palette navigates to settings via the action item", async ({ page }) => {
    await page.keyboard.press("ControlOrMeta+K")
    const input = page.getByPlaceholder(/search books, shelves/i)
    await input.fill("settings")
    await page.getByRole("option", { name: /^settings$/i }).first().click()
    await expect(page).toHaveURL("/settings")
  })
})
```

> **Note:** cmdk renders items as `role="option"` (not `role="button"`). If your version of cmdk emits a different role, run `make e2e-ui` to inspect the live ARIA tree and adjust the selector.

- [ ] **Step 3: Run the spec against a live dev stack**

Run:

```bash
make up   # in one terminal — wait for "listening on :6060"
make e2e -- e2e/tests/command-palette.spec.ts   # in another
```

Expected: both tests PASS. Stop the dev stack when done (Ctrl-C).

- [ ] **Step 4: Commit**

```bash
git add e2e/tests/command-palette.spec.ts
git commit -m "test(e2e): cover ⌘K palette navigation"
```

---

## Task 18: Final verification

- [ ] **Step 1: Run the full local CI gauntlet**

Run: `make ci-local`
Expected: PASS for go-lint, ui-lint, ui-typecheck, test, ui-test.

- [ ] **Step 2: Manual end-to-end check**

`make up`, then verify in the browser:

1. /library page — type "a" in the search field. Grid filters AND a popover shows top books. Picking one navigates to its detail page.
2. ⌘K from any authed page — palette opens. Quick actions, Navigation visible. Typing "settings" + Enter goes to /settings.
3. ⌘K, type a real book title, Enter — lands on the book page.
4. ⌘K, "new shelf", Enter — shelf-draft dialog opens.
5. ⌘K, "sign out", Enter — redirected to /login.
6. Sign in as a non-admin (create one via Settings → Users first if needed) — ⌘K, confirm "Library scan" is NOT in the list.

If any check fails, fix and re-commit before opening a PR.

---

## Follow-ups (out of scope here)

1. Backfill backend tests for `internal/service/search` and `internal/handler/Search` when the project gains an integration test harness with a test database.
2. Add a dedicated `?section=libraries` deep link on `/settings` so the "Library scan" action lands on the right panel instead of the page top.
3. Consider a recent / pinned items section in the palette once usage data justifies the persistence layer.
4. If the new endpoint shows up as hot in production, add a covering index on `lower(shelves.name)` and `lower(libraries.name)` to speed up the ILIKE.
