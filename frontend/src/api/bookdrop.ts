import { api } from './client';
import type { BookDetail } from './books';

// Mirrors internal/handler/bookdrop.go bookdropDTO.
export type BookDropState =
  | 'discovered'
  | 'processing'
  | 'ready'
  | 'failed'
  | 'imported'
  | 'rejected';

export type BookDropItem = {
  id: string;
  filename: string;
  path: string;
  fileSize: number;
  format: string;
  state: BookDropState;
  progress: number;
  errorMsg?: string;
  title?: string;
  author?: string;
  description?: string;
  language?: string;
  hasCover: boolean;
  coverMime?: string;
  bookId?: string;
  discoveredAt: string;
  updatedAt: string;
};

export async function fetchBookDrop(): Promise<BookDropItem[]> {
  const { items } = await api<{ items: BookDropItem[] }>('/api/v1/bookdrop');
  return items;
}

export async function approveBookDrop(
  id: string,
  libraryId?: string,
): Promise<BookDetail> {
  const { book } = await api<{ book: BookDetail }>(
    `/api/v1/bookdrop/${id}/approve`,
    {
      method: 'POST',
      body: libraryId ? JSON.stringify({ libraryId }) : undefined,
    },
  );
  return book;
}

export async function rejectBookDrop(id: string): Promise<void> {
  await api<void>(`/api/v1/bookdrop/${id}/reject`, { method: 'POST' });
}

export const bookdropQueryKey = ['bookdrop'] as const;

export const bookdropCoverUrl = (id: string) => `/api/v1/bookdrop/${id}/cover`;
