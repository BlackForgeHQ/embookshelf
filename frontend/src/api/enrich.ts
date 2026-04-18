import { api } from './client';

// Mirrors internal/handler/enrich.go enrichMatchDTO.
export type EnrichMatch = {
  source: 'google_books' | 'open_library' | string;
  sourceId: string;
  title: string;
  authors: string[];
  description?: string;
  publisher?: string;
  year?: number;
  isbn?: string;
  series?: string;
  categories?: string[];
  language?: string;
  coverUrl?: string;
  confidence: number;
};

export type EnrichResult = {
  query: { title: string; author: string; isbn: string };
  matches: EnrichMatch[];
};

export type EnrichQuery = {
  title?: string;
  author?: string;
  isbn?: string;
};

export async function fetchEnrichment(
  bookId: string,
  q: EnrichQuery = {},
): Promise<EnrichResult> {
  const qs = new URLSearchParams();
  if (q.title) qs.set('title', q.title);
  if (q.author) qs.set('author', q.author);
  if (q.isbn) qs.set('isbn', q.isbn);
  const query = qs.toString();
  const path = query
    ? `/api/v1/books/${bookId}/enrich?${query}`
    : `/api/v1/books/${bookId}/enrich`;
  return api<EnrichResult>(path);
}

export async function applyCoverFromUrl(
  bookId: string,
  url: string,
): Promise<{ coverMime: string }> {
  return api<{ coverMime: string }>(
    `/api/v1/books/${bookId}/cover-from-url`,
    {
      method: 'POST',
      body: JSON.stringify({ url }),
    },
  );
}

export const enrichQueryKey = (bookId: string, q: EnrichQuery) =>
  ['enrich', bookId, q] as const;
