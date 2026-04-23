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
  // IDs of providers the server actually queried for this request —
  // excludes disabled ones. Empty when no provider is enabled or when
  // the query was blank (server short-circuits).
  providers: string[];
};

// Wire-side provider IDs (matches internal/provider/provider.go Source
// constants) mapped to display labels. Used for the enrichment panel's
// empty-state copy so the UI lists what was actually searched.
export const PROVIDER_LABELS: Record<string, string> = {
  google_books: 'Google Books',
  open_library: 'Open Library',
  amazon: 'Amazon',
  duckduckgo: 'DuckDuckGo',
};

export function formatProviderList(ids: readonly string[]): string {
  const labels = ids.map((id) => PROVIDER_LABELS[id] ?? id);
  if (labels.length === 0) return '';
  if (labels.length === 1) return labels[0];
  if (labels.length === 2) return `${labels[0]} or ${labels[1]}`;
  return `${labels.slice(0, -1).join(', ')}, or ${labels[labels.length - 1]}`;
}

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
