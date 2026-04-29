import { api } from "./client"

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
// mutation flows to expose.
export type Shelf = {
  id: string
  name: string
  slug: string
  accent: string
  bookCount: number
  isSmart: boolean
  rule?: ShelfRule
  createdAt: string
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

export async function toggleBookFieldLocks(
  id: string,
  locks: Partial<Record<LockField, boolean>>
): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(
    `/api/v1/books/${id}/metadata/locks`,
    {
      method: "PUT",
      body: JSON.stringify({ locks }),
    }
  )
  return book
}

export type BookDetail = Book & { shelves: Array<string> }

export type BooksQuery = {
  library?: string
  shelf?: string
  q?: string
  format?: Array<string> // joined with commas on the wire
  sort?: "title" | "author" | "recent" | "year" | "rating"
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
  const query = qs.toString()
  return query ? `/api/v1/books?${query}` : "/api/v1/books"
}

export async function fetchLibraries(): Promise<Array<Library>> {
  const { libraries } = await api<{ libraries: Array<Library> }>(
    "/api/v1/libraries"
  )
  return libraries
}

export async function fetchShelves(): Promise<Array<Shelf>> {
  const { shelves } = await api<{ shelves: Array<Shelf> }>("/api/v1/shelves")
  return shelves
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

export async function patchBook(
  id: string,
  patch: BookPatch
): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(`/api/v1/books/${id}`, {
    method: "PATCH",
    body: JSON.stringify(patch),
  })
  return book
}

// deleteBook hard-deletes a book (and its cover + source file). Admin-only
// on the backend; non-admin callers get 403.
export async function deleteBook(id: string): Promise<void> {
  await api<void>(`/api/v1/books/${id}`, { method: "DELETE" })
}

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

export async function addBookToShelf(
  bookId: string,
  shelfSlug: string
): Promise<void> {
  await api<void>(
    `/api/v1/books/${bookId}/shelves/${encodeURIComponent(shelfSlug)}`,
    { method: "POST" }
  )
}

export async function removeBookFromShelf(
  bookId: string,
  shelfSlug: string
): Promise<void> {
  await api<void>(
    `/api/v1/books/${bookId}/shelves/${encodeURIComponent(shelfSlug)}`,
    { method: "DELETE" }
  )
}

export async function createShelf(
  name: string,
  accent?: string
): Promise<Shelf> {
  const { shelf } = await api<{ shelf: Shelf }>("/api/v1/shelves", {
    method: "POST",
    body: JSON.stringify({ name, accent }),
  })
  return shelf
}

// createSmartShelf attaches a rule on creation — the backend switches on
// `rule` being present to flip is_smart and validate the payload.
export async function createSmartShelf(
  name: string,
  rule: ShelfRule,
  accent?: string
): Promise<Shelf> {
  const { shelf } = await api<{ shelf: Shelf }>("/api/v1/shelves", {
    method: "POST",
    body: JSON.stringify({ name, accent, rule }),
  })
  return shelf
}

// updateShelf lets callers rename + recolor + (for smart shelves) edit
// the rule. ruleSet disambiguates "don't touch the rule" from a rule
// payload — see internal/handler/shelves.go patchShelfReq.
export async function updateShelf(
  slug: string,
  body: { name?: string; accent?: string; rule?: ShelfRule; ruleSet?: boolean }
): Promise<Shelf> {
  const { shelf } = await api<{ shelf: Shelf }>(
    `/api/v1/shelves/${encodeURIComponent(slug)}`,
    {
      method: "PATCH",
      body: JSON.stringify(body),
    }
  )
  return shelf
}

export async function deleteShelf(slug: string): Promise<void> {
  await api<void>(`/api/v1/shelves/${encodeURIComponent(slug)}`, {
    method: "DELETE",
  })
}

// Stable query keys — share them across components so a mutation can
// invalidate a list and the detail view in one call.
export const librariesQueryKey = ["libraries"] as const
export const shelvesQueryKey = ["shelves"] as const
export const booksQueryKey = (params: BooksQuery = {}) =>
  ["books", params] as const
export const bookQueryKey = (id: string) => ["book", id] as const

// Cover URL helper — the <img> tag fetches directly from this path; no
// TanStack Query wrapper needed.
export const bookCoverUrl = (id: string) => `/api/v1/books/${id}/cover`
