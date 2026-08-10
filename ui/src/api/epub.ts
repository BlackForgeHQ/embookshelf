import { api } from "./client"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"

// Mirrors internal/handler/epub_rendition.go epubRenditionDTO. State
// "none" covers both "never generated" and "generated but the file has
// since gone" — the UI offers Generate for both. Error is the worker's
// message verbatim, including a chained markdown-conversion failure
// (ADR-0034 §5).
export type EpubRenditionStatus = {
  state: "none" | "pending" | "running" | "ready" | "failed"
  error?: string
  converterVersion?: string
  stale: boolean
  updatedAt?: string
}

export const bookEpubQueryKey = (bookId: string) =>
  ["books", bookId, "epub"] as const

export const bookEpubQuery = (bookId: string) =>
  defineQuery({
    key: bookEpubQueryKey(bookId),
    fn: (): Promise<EpubRenditionStatus> =>
      api<EpubRenditionStatus>(
        `/api/v1/books/${encodeURIComponent(bookId)}/epub`
      ),
  })

// generateBookEpub queues a render; 202 means the chain is moving —
// conversion first if the markdown rendition is missing.
export const generateBookEpub = defineMutation({
  fn: (bookId: string): Promise<void> =>
    api<void>(`/api/v1/books/${encodeURIComponent(bookId)}/epub`, {
      method: "POST",
    }),
  invalidates: (bookId) => [bookEpubQueryKey(bookId)],
})
