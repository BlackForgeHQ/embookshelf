import { api } from "./client"
import { defineMutation } from "./mutation"

// Mirrors internal/handler/library.go libraryDTO.
export type Library = {
  id: string
  name: string
  slug: string
  bookCount: number
  createdAt: string
}

// Mirrors internal/model/shelf_rule.go on the wire.
export type RuleMatch = "all" | "any"
export type RuleField =
  | "title"
  | "author"
  | "year"
  | "rating"
  | "format"
  | "series"
  | "tags"
  | "progress"
export type RuleOp =
  | "eq"
  | "ne"
  | "lt"
  | "lte"
  | "gt"
  | "gte"
  | "contains"
  | "starts_with"

export type ShelfPredicate = {
  field: RuleField
  op: RuleOp
  value: string | number
}

export type ShelfRule = {
  match: RuleMatch
  predicates: Array<ShelfPredicate>
}

// Mirrors internal/handler/shelves.go shelfDTO. `rule` is only present
// on smart shelves; the UI keys off `isSmart` when deciding whether to
// surface the shelf in the "Magic Shelves" section and which
// mutation flows to expose. Public shelves arrive with their slug
// already prefixed `public:` and `isPublic=true`; viewers other than
// the owner also get `ownerName` for the sidebar tooltip.
export type Shelf = {
  id: string
  name: string
  slug: string
  accent: string
  icon: string
  bookCount: number
  isSmart: boolean
  isPublic: boolean
  ownerName?: string
  rule?: ShelfRule
  createdAt: string
}

// PUBLIC_SLUG_PREFIX mirrors internal/service/shelf.go. Kept here so
// the sidebar / realtime layer can identify public shelves without a
// dedicated server roundtrip.
export const PUBLIC_SLUG_PREFIX = "public:"

export function isPublicShelfSlug(slug: string): boolean {
  return slug.startsWith(PUBLIC_SLUG_PREFIX)
}

// Mirrors internal/handler/library.go bookDTO. Progress is 0..1 on the wire.
export type Book = {
  id: string
  libraryId: string
  title: string
  subtitle?: string
  author: string
  format: string
  year: number
  // YYYY-MM-DD, empty/undefined when unknown.
  publishDate?: string
  language?: string
  progress: number
  resumeCfi?: string
  rating: number
  palette: string
  description?: string
  isbn?: string
  isbn10?: string
  publisher?: string
  series?: string
  seriesNum?: number
  seriesTotal?: number
  genres: Array<string>
  moods: Array<string>
  tags: Array<string>
  ageRating?: string
  contentRating?: string
  pages?: number
  // tri-state: null/undefined = unset ("No Value"), true/false = explicit.
  publicReviews?: boolean | null
  hasCover: boolean
  coverMime?: string
  // Short cache-buster derived from the cover bytes hash. Append as
  // ?v=… on the cover URL so a re-uploaded cover invalidates the
  // browser cache without forcing the API to drop its long max-age.
  coverVersion?: string
  addedAt: string
  // Audiobook fields. durationSeconds is undefined for non-audio formats
  // and may also be undefined for audio when the processor couldn't
  // determine a duration (no XING header, no MP4 mvhd atom).
  durationSeconds?: number
  narrator?: string
  chapters?: Array<Chapter>
  // Sparse map — only locked fields appear. Keys match LockField.
  locks?: Partial<Record<LockField, boolean>>
}

// Mirrors internal/handler/library.go chapterDTO. Times are seconds.
export type Chapter = {
  title: string
  startS: number
  endS: number
}

// Field keys accepted by PUT /books/:id/metadata/locks. Keep in sync
// with model.LockFields in internal/model/book.go.
export type LockField =
  | "title"
  | "subtitle"
  | "author"
  | "description"
  | "publisher"
  | "series"
  | "isbn"
  | "isbn10"
  | "language"
  | "publishDate"
  | "genres"
  | "moods"
  | "tags"
  | "pages"
  | "cover"

export const toggleBookFieldLocks = defineMutation({
  fn: async (args: {
    id: string
    locks: Partial<Record<LockField, boolean>>
  }): Promise<BookDetail> => {
    const { book } = await api<{ book: BookDetail }>(
      `/api/v1/books/${args.id}/metadata/locks`,
      {
        method: "PUT",
        body: JSON.stringify({ locks: args.locks }),
      }
    )
    return book
  },
  invalidates: (args) => [bookQueryKey(args.id)],
})

export type BookDetail = Book & { shelves: Array<string> }

export type BooksQuery = {
  library?: string
  shelf?: string
  q?: string
  format?: Array<string> // joined with commas on the wire
  sort?: "title" | "author" | "recent" | "year" | "rating"
  unshelved?: boolean
}

function buildBooksPath(params: BooksQuery): string {
  const qs = new URLSearchParams()
  if (params.library) qs.set("library", params.library)
  if (params.shelf) qs.set("shelf", params.shelf)
  if (params.q) qs.set("q", params.q)
  if (params.format && params.format.length > 0) {
    qs.set("format", params.format.join(","))
  }
  if (params.sort) qs.set("sort", params.sort)
  if (params.unshelved) qs.set("unshelved", "1")
  const query = qs.toString()
  return query ? `/api/v1/books?${query}` : "/api/v1/books"
}

export async function fetchLibraries(): Promise<Array<Library>> {
  const { libraries } = await api<{ libraries: Array<Library> }>(
    "/api/v1/libraries"
  )
  return libraries
}

// ShelvesPayload is the wire shape of /api/v1/shelves: the shelf list
// plus a live count for the "Unshelved" virtual view (books not on any
// of the user's regular non-system shelves). The sidebar reads both.
export type ShelvesPayload = {
  shelves: Array<Shelf>
  unshelvedCount: number
}

export async function fetchShelves(): Promise<ShelvesPayload> {
  return api<ShelvesPayload>("/api/v1/shelves")
}

export async function fetchBooks(params: BooksQuery = {}): Promise<{
  books: Array<Book>
  total: number
}> {
  return api<{ books: Array<Book>; total: number }>(buildBooksPath(params))
}

export async function fetchBook(id: string): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(`/api/v1/books/${id}`)
  return book
}

// All fields optional — missing fields preserve the existing row.
// publishDate is "YYYY-MM-DD" or "" to clear. publicReviewsClear lets
// the UI explicitly reset the tri-state (overrides any publicReviews
// set in the same payload).
export type BookPatch = {
  title?: string
  subtitle?: string
  author?: string
  format?: string
  year?: number
  publishDate?: string
  language?: string
  rating?: number
  palette?: string
  description?: string
  isbn?: string
  isbn10?: string
  publisher?: string
  series?: string
  seriesNum?: number
  seriesTotal?: number
  genres?: Array<string>
  moods?: Array<string>
  tags?: Array<string>
  ageRating?: string
  contentRating?: string
  pages?: number
  publicReviews?: boolean
  publicReviewsClear?: boolean
}

export const patchBook = defineMutation({
  fn: async (args: { id: string; patch: BookPatch }): Promise<BookDetail> => {
    const { book } = await api<{ book: BookDetail }>(
      `/api/v1/books/${args.id}`,
      {
        method: "PATCH",
        body: JSON.stringify(args.patch),
      }
    )
    return book
  },
  invalidates: (args) => [bookQueryKey(args.id), booksQueryKey()],
})

// deleteBook hard-deletes a book (and its cover + source file). Admin-only
// on the backend; non-admin callers get 403.
export const deleteBook = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/books/${id}`, { method: "DELETE" }),
  invalidates: (id) => [bookQueryKey(id), booksQueryKey(), librariesQueryKey],
})

// updateProgress is called from reader event handlers and timers, not
// from useMutation. Keep it as a plain async function; the reader has
// no toast/invalidate orchestration to share with form-driven mutations.
export async function updateProgress(
  id: string,
  progress: number,
  resumeCfi?: string
): Promise<void> {
  await api<void>(`/api/v1/books/${id}/progress`, {
    method: "POST",
    body: JSON.stringify({ progress, resumeCfi }),
  })
}

// fetchComicPageCount returns the total page count for a CBZ book. The
// reader needs this before it can size navigation; pages themselves are
// fetched lazily as <img src="/api/v1/books/:id/pages/:n">.
export async function fetchComicPageCount(id: string): Promise<number> {
  const r = await api<{ count: number }>(`/api/v1/books/${id}/pages`)
  return r.count
}

export function comicPageURL(id: string, page: number): string {
  return `/api/v1/books/${id}/pages/${page}`
}

export const addBookToShelf = defineMutation({
  fn: (args: { bookId: string; shelfSlug: string }): Promise<void> =>
    api<void>(
      `/api/v1/books/${args.bookId}/shelves/${encodeURIComponent(args.shelfSlug)}`,
      { method: "POST" }
    ),
  invalidates: (args) => [
    bookQueryKey(args.bookId),
    booksQueryKey(),
    shelvesQueryKey,
  ],
})

export const removeBookFromShelf = defineMutation({
  fn: (args: { bookId: string; shelfSlug: string }): Promise<void> =>
    api<void>(
      `/api/v1/books/${args.bookId}/shelves/${encodeURIComponent(args.shelfSlug)}`,
      { method: "DELETE" }
    ),
  invalidates: (args) => [
    bookQueryKey(args.bookId),
    booksQueryKey(),
    shelvesQueryKey,
  ],
})

// Stable query keys — share them across components so a mutation can
// invalidate a list and the detail view in one call.
export const librariesQueryKey = ["libraries"] as const
export const shelvesQueryKey = ["shelves"] as const
export const booksQueryKey = (params: BooksQuery = {}) =>
  ["books", params] as const
export const bookQueryKey = (id: string) => ["book", id] as const

export const createShelf = defineMutation({
  fn: async (args: {
    name: string
    accent?: string
    icon?: string
  }): Promise<Shelf> => {
    const { shelf } = await api<{ shelf: Shelf }>("/api/v1/shelves", {
      method: "POST",
      body: JSON.stringify({
        name: args.name,
        accent: args.accent,
        icon: args.icon,
      }),
    })
    return shelf
  },
  invalidates: [shelvesQueryKey],
})

// createSmartShelf attaches a rule on creation — the backend switches on
// `rule` being present to flip is_smart and validate the payload.
export const createSmartShelf = defineMutation({
  fn: async (args: {
    name: string
    rule: ShelfRule
    accent?: string
    icon?: string
  }): Promise<Shelf> => {
    const { shelf } = await api<{ shelf: Shelf }>("/api/v1/shelves", {
      method: "POST",
      body: JSON.stringify({
        name: args.name,
        accent: args.accent,
        icon: args.icon,
        rule: args.rule,
      }),
    })
    return shelf
  },
  invalidates: [shelvesQueryKey],
})

// updateShelf lets callers rename + recolor + (for smart shelves) edit
// the rule. ruleSet disambiguates "don't touch the rule" from a rule
// payload — see internal/handler/shelves.go patchShelfReq.
export const updateShelf = defineMutation({
  fn: async (args: {
    slug: string
    body: {
      name?: string
      accent?: string
      icon?: string
      rule?: ShelfRule
      ruleSet?: boolean
    }
  }): Promise<Shelf> => {
    const { shelf } = await api<{ shelf: Shelf }>(
      `/api/v1/shelves/${encodeURIComponent(args.slug)}`,
      {
        method: "PATCH",
        body: JSON.stringify(args.body),
      }
    )
    return shelf
  },
  invalidates: [shelvesQueryKey],
})

export const deleteShelf = defineMutation({
  fn: (slug: string): Promise<void> =>
    api<void>(`/api/v1/shelves/${encodeURIComponent(slug)}`, {
      method: "DELETE",
    }),
  invalidates: [shelvesQueryKey],
})

// publishShelf flips a shelf's is_public flag. Admin-only at the
// server (403 from any other caller); owner-only beyond that. Pass
// the canonical slug — public-prefixed once published, bare otherwise.
export const publishShelf = defineMutation({
  fn: async (args: { slug: string; isPublic: boolean }): Promise<Shelf> => {
    const { shelf } = await api<{ shelf: Shelf }>(
      `/api/v1/shelves/${encodeURIComponent(args.slug)}/publish`,
      {
        method: "PUT",
        body: JSON.stringify({ public: args.isPublic }),
      }
    )
    return shelf
  },
  // Public flag toggles change shelf membership visibility; bust the
  // shelves list and the books-on-this-shelf query.
  invalidates: (args) => [shelvesQueryKey, booksQueryKey({ shelf: args.slug })],
})

// Cover URL helper — the <img> tag fetches directly from this path; no
// TanStack Query wrapper needed.
export const bookCoverUrl = (id: string, version?: string) =>
  version
    ? `/api/v1/books/${id}/cover?v=${encodeURIComponent(version)}`
    : `/api/v1/books/${id}/cover`

// Amazon's Send-to-Kindle service accepts EPUB and PDF only. Mirrors
// the eligibility check on internal/handler/kindle.go so the UI can
// disable the action up front instead of round-tripping for a 415.
export function isKindleEligibleFormat(format: string): boolean {
  const f = format.toLowerCase()
  return f === "epub" || f === "pdf"
}

export const sendBookToKindle = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/books/${id}/send-to-kindle`, { method: "POST" }),
  invalidates: [],
})
