# The UI is a Next.js static export served by the Go binary

The UI moved from TanStack Start (SPA mode, Vite) to Next.js 16 (App Router, Turbopack). The question this ADR settles is how a framework built around a Node server fits a product whose only server is a single Go binary, and what the move costs.

## Status

accepted (2026-11-18)

## Decision

### 1. `output: "export"`, embedded as before

`next build` emits a static export to `ui/out/`. `ui/scripts/sync-dist.ts` copies it into `internal/staticfs/dist/`, and the binary embeds it with `//go:embed all:dist`, as it embedded the Vite build. No Node process runs in production, the single binary stays single, and the Docker image's runtime stage is unchanged.

The rejected shape is a Next.js server beside the Go one. It would buy server components that fetch data, middleware, and real dynamic routes. None of those pays for a second runtime, a second process to supervise and a reverse proxy in front of both, in a self-hosted, auth-walled app whose data already comes from `/api/v1` with the browser's cookie.

What the export gives up: server components that read data, route handlers, middleware, `next/image` optimization and incremental rendering. Every page is a client component (`"use client"`); the root layout is the one server component, and it holds only metadata and the providers.

### 2. Dynamic routes are prerendered once, under `_`

A static export must know every value of a dynamic segment at build time. Book ids are not known. Each `[id]` segment exports `generateStaticParams` returning a single placeholder, `_` (`PLACEHOLDER_ID` in `ui/src/lib/routes.ts`), so the build emits `book/_.html`, `book/_/edit.html`, `read/_.html` and the router payloads beside them.

The Go server resolves a request against the export segment by segment (`resolveExportPath`, `internal/handler/spa.go`): an existing segment is taken as is, a missing one falls back to the directory's `_`, and the last segment may name a page by its `.html` file. A placeholder keeps the extension it replaces, because a client-side navigation asks for `/book/42.txt` and must get `book/_.txt`, not the page. The same walk answers the router's segment payloads (`/book/42/__next._tree.txt`), so moving between books never falls back to a full page load. Anything the export has no file for gets `404.html` with status 404, where the old fallback answered every unknown path with the shell and a 200.

The page reads the real id with `useRouteId` (`ui/src/hooks/useRoute.ts`), which parses `usePathname`. `useParams` is not used for ids: in the export it reports the placeholder the page was prerendered for.

The rejected alternative was query-string ids (`/book?id=42`). It needs no server mapping, but it changes every URL the app, its bookmarks and the backend emit, for the sake of a dozen lines of Go.

### 3. Typed routing becomes path builders and parsers

TanStack Router typed `<Link to params search>` and `validateSearch`. Next.js takes plain strings. The typed layer is now `paths` (builders) and `parse*Search` (parsers) in `ui/src/lib/routes.ts`, read with `useRouteSearch`. A URL is built and read in one place per route, rather than in string literals at each call site.

### 4. Navigation blocking is the app's own

Next.js has no `useBlocker`, and the metadata editor's unsaved-changes prompt depends on one. `ui/src/lib/navigation.ts` keeps a registry of guards. In-app link clicks are intercepted at the document in the capture phase. `router.push`/`replace` go through `useAppRouter`, which every call site uses instead of `next/navigation`'s `useRouter`. Back/forward is cancelled through the Navigation API where the browser has it. A full unload stays the page's own `beforeunload`.

### 5. Authentication gating moves into the `(app)` layout

`beforeLoad` redirects ran before render. The `(app)` route-group layout now renders nothing until `/me` answers and replaces the URL with `/login?next=…` on a signed-out answer. The prerender and the first client render both see the pending state, so the exported HTML and the hydrated tree agree.

## Consequences

- Nothing may touch browser-only globals at module scope. The prerender evaluates every page module in Node. pdf.js is loaded on first use (`ui/src/lib/pdfjs.ts`) for exactly this reason.
- `next dev` runs as a normal server (rewrites need one) and the export applies only to `next build`. Dev disables response compression, which would otherwise buffer the proxied `/events` stream.
- `next-env.d.ts` is generated; `bun run typecheck` runs `next typegen` first.
- Vite stays as Vitest's runner only.
- `next dev` writes `ui/AGENTS.md` on every run; it is committed so the tree stays clean.
