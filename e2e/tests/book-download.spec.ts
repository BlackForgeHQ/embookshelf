import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

// Pick any book from the shared dev library so these specs don't care
// which seed is loaded — they only need a single ingestable row to hit
// /file with.
async function anyBook(
  adminApi: APIRequestContext,
): Promise<{ id: string; title: string }> {
  const res = await adminApi.get('/api/v1/books?limit=1');
  expect(res.ok()).toBeTruthy();
  const { books } = (await res.json()) as {
    books: { id: string; title: string }[];
  };
  expect(books.length).toBeGreaterThan(0);
  return books[0];
}

test.describe('book detail · download', () => {
  test('Download link points at /file?download=1 with the download attribute', async ({
    page,
    adminApi,
  }) => {
    const book = await anyBook(adminApi);

    await page.goto(`/book/${book.id}`);

    // The Download button is rendered as an <a> (shadcn Button asChild)
    // so it must expose role=link, not role=button.
    const link = page.getByRole('link', { name: /Download/ });
    await expect(link).toHaveAttribute(
      'href',
      `/api/v1/books/${book.id}/file?download=1`,
    );
    await expect(link).toHaveAttribute('download', /.*/);
  });

  test('hitting the download URL returns Content-Disposition: attachment with the book filename', async ({
    adminApi,
  }) => {
    const book = await anyBook(adminApi);

    const res = await adminApi.get(`/api/v1/books/${book.id}/file?download=1`);
    expect(res.ok()).toBeTruthy();
    const disposition = res.headers()['content-disposition'] ?? '';
    // RFC 6266 — the server writes `attachment; filename="..."; filename*=UTF-8''...`
    expect(disposition.toLowerCase()).toContain('attachment');
    expect(disposition).toMatch(/filename=/i);
  });

  test('without ?download=1 the same URL streams inline for the in-app reader', async ({
    adminApi,
  }) => {
    const book = await anyBook(adminApi);

    const res = await adminApi.get(`/api/v1/books/${book.id}/file`);
    expect(res.ok()).toBeTruthy();
    // No attachment — the reader pulls the body directly.
    const disposition = res.headers()['content-disposition'] ?? '';
    expect(disposition.toLowerCase()).not.toContain('attachment');
  });
});
