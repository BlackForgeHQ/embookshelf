// The UI is a Next.js static export embedded in the Go binary, so every
// URL the app links to is built here: Next's <Link> and router take plain
// strings, and these builders are what keep those strings well-formed.

/**
 * The one id a dynamic segment is prerendered for. The build emits
 * `book/_.html`, `read/_.html` and friends; the Go server resolves any real
 * id to those files (internal/handler/router.go, mountSPA), and the page
 * reads the actual id back out of the URL with useRouteId
 * (src/hooks/useRoute.ts).
 */
export const PLACEHOLDER_ID = "_"

/** generateStaticParams for every `[id]` segment. */
export function placeholderParams(): Array<{ id: string }> {
  return [{ id: PLACEHOLDER_ID }]
}

type SearchValue = string | number | boolean | null | undefined

/** path + query string, dropping empty values so URLs stay clean. */
export function withSearch(
  path: string,
  search?: Record<string, SearchValue>
): string {
  if (!search) return path
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(search)) {
    if (value === undefined || value === null || value === "") continue
    params.set(key, String(value))
  }
  const qs = params.toString()
  return qs ? `${path}?${qs}` : path
}

function oneOf<T extends string>(
  value: string | null,
  allowed: ReadonlyArray<T>
): T | undefined {
  return allowed.includes(value as T) ? (value as T) : undefined
}

function str(value: string | null): string | undefined {
  return value === null || value === "" ? undefined : value
}

// ---- /library ---------------------------------------------------------

export const LIBRARY_LAYOUTS = ["shelf", "grid", "list"] as const
export const LIBRARY_SORTS = ["added", "title", "author", "rating"] as const
export type LibraryLayout = (typeof LIBRARY_LAYOUTS)[number]
export type LibrarySort = (typeof LIBRARY_SORTS)[number]

export type LibrarySearch = {
  shelf?: string
  library?: string
  layout?: LibraryLayout
  // `?unshelved=1` is the canonical truthy form. Anything else drops to
  // undefined so the URL stays clean when the filter is off.
  unshelved?: "1"
  sort?: LibrarySort
}

export function parseLibrarySearch(p: URLSearchParams): LibrarySearch {
  return {
    shelf: str(p.get("shelf")),
    library: str(p.get("library")),
    layout: oneOf(p.get("layout"), LIBRARY_LAYOUTS),
    unshelved: p.get("unshelved") === "1" ? "1" : undefined,
    sort: oneOf(p.get("sort"), LIBRARY_SORTS),
  }
}

// ---- /account ---------------------------------------------------------

export const ACCOUNT_SECTIONS = [
  "account",
  "reading",
  "devices",
  "shelves",
  "mcp",
] as const
export type AccountSection = (typeof ACCOUNT_SECTIONS)[number]

export type AccountSearch = {
  section?: AccountSection
  // Redirect-back signals from the OIDC link callback. The panel
  // renders a toast and clears these via a replace navigation so the URL
  // doesn't keep firing the toast on refresh.
  linked?: string
  error?: string
}

export function parseAccountSearch(p: URLSearchParams): AccountSearch {
  return {
    section: oneOf(p.get("section"), ACCOUNT_SECTIONS),
    linked: str(p.get("linked")),
    error: str(p.get("error")),
  }
}

// ---- /login -----------------------------------------------------------

export type LoginSearch = {
  next?: string
  mode: "login" | "signup"
  oidcError?: string
  local: boolean
}

export function parseLoginSearch(p: URLSearchParams): LoginSearch {
  return {
    next: str(p.get("next")),
    mode: p.get("mode") === "signup" ? "signup" : "login",
    oidcError: str(p.get("oidcError")),
    local: p.get("local") === "true",
  }
}

/**
 * Where to send a user after signing in: the `next` they arrived with if
 * it is a same-origin path, the dashboard otherwise. Protocol-relative
 * (`//host`) and backslash (`/\host`) forms are off-origin to a browser,
 * and the login page itself would loop.
 */
export function safeNext(raw: string | undefined): string {
  if (!raw) return "/"
  if (!raw.startsWith("/")) return "/"
  if (raw.startsWith("//") || raw.startsWith("/\\")) return "/"
  if (
    raw === "/login" ||
    raw.startsWith("/login?") ||
    raw.startsWith("/login/")
  )
    return "/"
  return raw
}

// ---- token pages (/reset, /accept-invite) -----------------------------

export type TokenSearch = { token?: string }

export function parseTokenSearch(p: URLSearchParams): TokenSearch {
  return { token: str(p.get("token")) }
}

// ---- builders ---------------------------------------------------------

const seg = encodeURIComponent

export const paths = {
  home: "/",
  library: (search?: LibrarySearch) => withSearch("/library", search),
  notebook: "/notebook",
  stats: "/stats",
  bookdrop: "/bookdrop",
  settings: "/settings",
  book: (id: string) => `/book/${seg(id)}`,
  bookEdit: (id: string) => `/book/${seg(id)}/edit`,
  bookFind: (id: string) => `/book/${seg(id)}/find`,
  read: (id: string) => `/read/${seg(id)}`,
  account: (search?: { section?: AccountSection }) =>
    withSearch("/account", search),
  login: (search?: { next?: string }) => withSearch("/login", search),
  loginPending: "/login/pending",
  forgotPassword: "/forgot-password",
} as const
