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
