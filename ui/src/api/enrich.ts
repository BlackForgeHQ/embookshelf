import { api } from './client';
import type { BookDetail } from './books';

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
  hardcover: 'Hardcover',
  goodreads: 'Goodreads',
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

// StreamEvent frames sent by the SSE-based fetch endpoint. The `done`
// frame is the terminator — callers should close the EventSource after
// receiving it so the connection gets released.
export type EnrichStreamEvent =
  | { type: 'match'; match: EnrichMatch }
  | { type: 'provider-error'; provider: string; error: string }
  | { type: 'done'; providers: string[] };

// streamEnrichment opens an SSE connection to the streaming fetch
// endpoint and invokes `onEvent` for each frame. Returns a cancel
// function that aborts the underlying EventSource. The browser carries
// session cookies on same-origin EventSource requests automatically.
export function streamEnrichment(
  bookId: string,
  q: EnrichQuery,
  onEvent: (ev: EnrichStreamEvent) => void,
): () => void {
  const qs = new URLSearchParams();
  if (q.title) qs.set('title', q.title);
  if (q.author) qs.set('author', q.author);
  if (q.isbn) qs.set('isbn', q.isbn);
  const query = qs.toString();
  const url = query
    ? `/api/v1/books/${bookId}/enrich/stream?${query}`
    : `/api/v1/books/${bookId}/enrich/stream`;
  const es = new EventSource(url, { withCredentials: true });
  es.addEventListener('match', (e) => {
    try {
      const match = JSON.parse((e as MessageEvent).data) as EnrichMatch;
      onEvent({ type: 'match', match });
    } catch {
      /* ignore malformed frame */
    }
  });
  es.addEventListener('provider-error', (e) => {
    try {
      const data = JSON.parse((e as MessageEvent).data) as {
        provider: string;
        error: string;
      };
      onEvent({ type: 'provider-error', ...data });
    } catch {
      /* ignore */
    }
  });
  es.addEventListener('done', (e) => {
    try {
      const data = JSON.parse((e as MessageEvent).data) as {
        providers: string[];
      };
      onEvent({ type: 'done', providers: data.providers ?? [] });
    } catch {
      onEvent({ type: 'done', providers: [] });
    }
    es.close();
  });
  es.onerror = () => {
    // Browsers reconnect automatically on transient errors; bail out
    // cleanly when the server has closed the stream or auth fails.
    if (es.readyState === EventSource.CLOSED) {
      onEvent({ type: 'done', providers: [] });
    }
  };
  return () => es.close();
}

// ApplyMatchBody mirrors internal/handler/enrich.go applyMetadataReq.
// All optional provider-derived fields are forwarded verbatim; the
// server consults per-field locks before writing.
export type ApplyMatchBody = Omit<EnrichMatch, 'confidence'> & {
  mergeCategories?: boolean;
  applyCover?: boolean;
};

export async function applyEnrichmentMatch(
  bookId: string,
  body: ApplyMatchBody,
): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(
    `/api/v1/books/${bookId}/metadata`,
    {
      method: 'PUT',
      body: JSON.stringify(body),
    },
  );
  return book;
}

export type ISBNLookupResult = { provider: string; match: EnrichMatch };

export async function lookupByISBN(isbn: string): Promise<ISBNLookupResult> {
  return api<ISBNLookupResult>('/api/v1/books/metadata/isbn-lookup', {
    method: 'POST',
    body: JSON.stringify({ isbn }),
  });
}
