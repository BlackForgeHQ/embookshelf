import { api } from "./client"
import { defineMutation } from "./mutation"

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

export const patchAnnotation = defineMutation({
  fn: async (args: {
    id: string
    body: PatchAnnotationInput
  }): Promise<Annotation> => {
    const { annotation } = await api<{ annotation: Annotation }>(
      `/api/v1/annotations/${args.id}`,
      {
        method: "PATCH",
        body: JSON.stringify(args.body),
      }
    )
    return annotation
  },
  // Caller must include the bookId-scoped key when known; we can't
  // recover bookId from the annotation id alone, so the recent feed
  // is the only universally invalidated cache here.
  invalidates: [recentAnnotationsQueryKey],
})

export const deleteAnnotation = defineMutation({
  fn: (args: { id: string; bookId: string }): Promise<void> =>
    api<void>(`/api/v1/annotations/${args.id}`, { method: "DELETE" }),
  invalidates: (args) => [
    bookAnnotationsQueryKey(args.bookId),
    recentAnnotationsQueryKey,
  ],
})
