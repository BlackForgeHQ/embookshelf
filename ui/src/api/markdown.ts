import { api } from "./client"
import { defineQuery } from "./query"

// Mirrors internal/handler/markdown_rendition.go markdownRenditionDTO.
// State "none" means no conversion was ever requested; `error` is the
// worker's message verbatim (ADR-0033 §5) — display it, don't rewrite
// it, because it is the thing the admin has to act on.
export type MarkdownRenditionStatus = {
  state: "none" | "pending" | "running" | "ready" | "failed"
  error?: string
  location?: string
  sizeBytes?: number
  converterVersion?: string
  // The page record (ADR-0040): absent when not recorded; missingPages
  // is "1,10-15" (1-based) and absent when nothing was missing.
  pageCount?: number
  missingPages?: string
  stale: boolean
  updatedAt?: string
}

export const bookMarkdownQueryKey = (bookId: string) =>
  ["books", bookId, "markdown"] as const

export const bookMarkdownQuery = (bookId: string) =>
  defineQuery({
    key: bookMarkdownQueryKey(bookId),
    fn: (): Promise<MarkdownRenditionStatus> =>
      api<MarkdownRenditionStatus>(
        `/api/v1/books/${encodeURIComponent(bookId)}/markdown`
      ),
  })
