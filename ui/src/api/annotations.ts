import { api } from './client';

// Mirrors internal/handler/annotations.go annotationDTO.
// `kind` is derived in the client — the server treats highlights and
// margin notes as the same row with different fields populated.
export type Annotation = {
  id: string;
  bookId: string;
  locator?: string;
  selectedText?: string;
  note?: string;
  color: string;
  createdAt: string;
  updatedAt: string;
};

export type AnnotationKind = 'highlight' | 'note' | 'highlight+note';

export function annotationKind(a: Annotation): AnnotationKind {
  const hasHighlight = !!a.selectedText;
  const hasNote = !!a.note;
  if (hasHighlight && hasNote) return 'highlight+note';
  if (hasHighlight) return 'highlight';
  return 'note';
}

export type CreateAnnotationInput = {
  locator?: string;
  selectedText?: string;
  note?: string;
  color?: string;
};

export type PatchAnnotationInput = {
  selectedText?: string;
  note?: string;
  color?: string;
};

export async function fetchBookAnnotations(bookId: string): Promise<Annotation[]> {
  const { annotations } = await api<{ annotations: Annotation[] }>(
    `/api/v1/books/${bookId}/annotations`,
  );
  return annotations;
}

export async function fetchRecentAnnotations(limit?: number): Promise<Annotation[]> {
  const qs = limit ? `?limit=${encodeURIComponent(limit)}` : '';
  const { annotations } = await api<{ annotations: Annotation[] }>(
    `/api/v1/annotations${qs}`,
  );
  return annotations;
}

export async function createAnnotation(
  bookId: string,
  body: CreateAnnotationInput,
): Promise<Annotation> {
  const { annotation } = await api<{ annotation: Annotation }>(
    `/api/v1/books/${bookId}/annotations`,
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
  );
  return annotation;
}

export async function patchAnnotation(
  id: string,
  body: PatchAnnotationInput,
): Promise<Annotation> {
  const { annotation } = await api<{ annotation: Annotation }>(
    `/api/v1/annotations/${id}`,
    {
      method: 'PATCH',
      body: JSON.stringify(body),
    },
  );
  return annotation;
}

export async function deleteAnnotation(id: string): Promise<void> {
  await api<void>(`/api/v1/annotations/${id}`, { method: 'DELETE' });
}

// Shared query keys so mutation onSuccess can invalidate the right caches
// in one call.
export const bookAnnotationsQueryKey = (bookId: string) =>
  ['annotations', bookId] as const;
export const recentAnnotationsQueryKey = ['annotations', 'recent'] as const;
