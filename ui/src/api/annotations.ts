import type { Locator } from "@/lib/locator"
import { encodeLocator, locatorLabel } from "@/lib/locator"
import { api } from "./client"
import { defineMutation } from "./mutation"
import { defineQuery } from "./query"

// Mirrors internal/handler/annotations.go annotationDTO.
// `kind` is derived in the client — the server treats highlights and
// margin notes as the same row with different fields populated.
export type Annotation = {
  id: string
  bookId: string
  locator?: string
  selectedText?: string
  note?: string
  color: string
  createdAt: string
  updatedAt: string
}

export type AnnotationKind = "highlight" | "note" | "highlight+note"

export function annotationKind(a: Annotation): AnnotationKind {
  const hasHighlight = !!a.selectedText
  const hasNote = !!a.note
  if (hasHighlight && hasNote) return "highlight+note"
  if (hasHighlight) return "highlight"
  return "note"
}

export type CreateAnnotationInput = {
  locator?: string
  selectedText?: string
  note?: string
  color?: string
}

export type PatchAnnotationInput = {
  selectedText?: string
  note?: string
  color?: string
}

export async function fetchBookAnnotations(
  bookId: string
): Promise<Array<Annotation>> {
  const { annotations } = await api<{ annotations: Array<Annotation> }>(
    `/api/v1/books/${bookId}/annotations`
  )
  return annotations
}

export async function fetchRecentAnnotations(
  limit?: number
): Promise<Array<Annotation>> {
  const qs = limit ? `?limit=${encodeURIComponent(limit)}` : ""
  const { annotations } = await api<{ annotations: Array<Annotation> }>(
    `/api/v1/annotations${qs}`
  )
  return annotations
}

// Shared query keys so mutation onSuccess can invalidate the right caches
// in one call.
export const bookAnnotationsQueryKey = (bookId: string) =>
  ["annotations", bookId] as const
export const recentAnnotationsQueryKey = ["annotations", "recent"] as const

export const bookAnnotationsQuery = (bookId: string) =>
  defineQuery({
    key: bookAnnotationsQueryKey(bookId),
    fn: () => fetchBookAnnotations(bookId),
  })

export const recentAnnotationsQuery = (limit?: number) =>
  defineQuery({
    key: recentAnnotationsQueryKey,
    fn: () => fetchRecentAnnotations(limit),
  })

export const createAnnotation = defineMutation({
  fn: async (args: {
    bookId: string
    body: CreateAnnotationInput
  }): Promise<Annotation> => {
    const { annotation } = await api<{ annotation: Annotation }>(
      `/api/v1/books/${args.bookId}/annotations`,
      {
        method: "POST",
        body: JSON.stringify(args.body),
      }
    )
    return annotation
  },
  invalidates: (args) => [
    bookAnnotationsQueryKey(args.bookId),
    recentAnnotationsQueryKey,
  ],
})

// A bookmark is a zero-text annotation at the current position, marked
// `color: "bookmark"` so the notebook can group it apart from highlights
// and notes. The annotations CHECK constraint requires `selected_text` or
// `note` to be non-empty, which is why the label is stored as the
// selected text rather than left blank.
//
// It was written out three times — once per reader shell — and each copy
// hand-wrote the invalidation this spec already declares, spelled its own
// label, and cast its own error. The shells hand over a decoded `Locator`
// now; encoding and labelling belong to `lib/locator.ts`, so a new
// position kind needs nothing here.
export const createBookmark = defineMutation({
  fn: (args: { bookId: string; locator: Locator }): Promise<Annotation> =>
    createAnnotation.fn({
      bookId: args.bookId,
      body: {
        locator: encodeLocator(args.locator),
        selectedText: bookmarkLabel(args.locator),
        color: "bookmark",
      },
    }),
  invalidates: (args) => [
    bookAnnotationsQueryKey(args.bookId),
    recentAnnotationsQueryKey,
  ],
})

// A CFI is opaque to a reader, so naming its format adds nothing to the
// word "Bookmark"; a page or a timestamp is worth saying, in the same
// words the notebook already uses for that position.
function bookmarkLabel(locator: Locator): string {
  if (locator.kind === "cfi" || locator.kind === "unknown") return "Bookmark"
  return `Bookmark · ${locatorLabel(encodeLocator(locator))}`
}

export const deleteAnnotation = defineMutation({
  fn: (args: { id: string; bookId: string }): Promise<void> =>
    api<void>(`/api/v1/annotations/${args.id}`, { method: "DELETE" }),
  invalidates: (args) => [
    bookAnnotationsQueryKey(args.bookId),
    recentAnnotationsQueryKey,
  ],
})
