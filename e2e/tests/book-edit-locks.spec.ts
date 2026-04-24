import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

async function anyBook(
  adminApi: APIRequestContext,
): Promise<{ id: string; title: string; locks?: Record<string, boolean> }> {
  const res = await adminApi.get('/api/v1/books?limit=1');
  expect(res.ok()).toBeTruthy();
  const { books } = (await res.json()) as {
    books: { id: string; title: string; locks?: Record<string, boolean> }[];
  };
  expect(books.length).toBeGreaterThan(0);
  return books[0];
}

async function resetLocks(api: APIRequestContext, id: string): Promise<void> {
  // Defensive reset — blanket-unlock the fields we touch in this spec so
  // the seeded book is restored regardless of how the test exits.
  await api.put(`/api/v1/books/${id}/metadata/locks`, {
    data: { locks: { title: false, author: false } },
  });
}

test.describe('book edit · per-field lock toggle', () => {
  test('clicking the title padlock flips its lock state and persists via the locks API', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(20_000);
    const book = await anyBook(adminApi);

    // Normalize the starting state so the click-to-lock assertion
    // below is deterministic regardless of what the previous run left.
    await resetLocks(adminApi, book.id);

    try {
      await page.goto(`/book/${book.id}/edit`);

      // Wait for the form to hydrate — the "Title" row is always the
      // first Row with that label. The LockToggle is a child button in
      // that row.
      const titleRow = page
        .getByText('Title', { exact: true })
        .first()
        .locator('..');
      const lockBtn = titleRow.getByRole('button').first();

      const patchCall = page.waitForResponse(
        (r) =>
          r.request().method() === 'PUT' &&
          r.url().includes(`/api/v1/books/${book.id}/metadata/locks`) &&
          r.ok(),
      );
      await lockBtn.click();
      await patchCall;

      // Server reflects the new locked=true state.
      const afterLock = await adminApi.get(`/api/v1/books/${book.id}`);
      const { book: lockedBook } = (await afterLock.json()) as {
        book: { locks?: Record<string, boolean> };
      };
      expect(lockedBook.locks?.title).toBe(true);

      // Click again to unlock — round-trip the state.
      const unlockCall = page.waitForResponse(
        (r) =>
          r.request().method() === 'PUT' &&
          r.url().includes(`/api/v1/books/${book.id}/metadata/locks`) &&
          r.ok(),
      );
      await lockBtn.click();
      await unlockCall;

      const afterUnlock = await adminApi.get(`/api/v1/books/${book.id}`);
      const { book: unlockedBook } = (await afterUnlock.json()) as {
        book: { locks?: Record<string, boolean> };
      };
      // Sparse payload — absent or false are both "unlocked".
      expect(unlockedBook.locks?.title ?? false).toBe(false);
    } finally {
      await resetLocks(adminApi, book.id);
    }
  });

  test('rejects an unknown lock field with 400 (contract guard)', async ({
    adminApi,
  }) => {
    const book = await anyBook(adminApi);

    const res = await adminApi.put(`/api/v1/books/${book.id}/metadata/locks`, {
      data: { locks: { notAField: true } },
    });
    expect(res.status()).toBe(400);
    const body = (await res.json()) as { error?: string };
    expect(body.error ?? '').toMatch(/unknown lock field/i);
  });
});
