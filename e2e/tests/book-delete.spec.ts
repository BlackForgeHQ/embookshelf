import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { dropFixture } from '../fixtures/bookdrop';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

type BookLite = { id: string; title: string; format: string };

// createBookViaBookdrop drops an EPUB, waits for it to reach 'ready', and
// approves it. Returns the created book. The file is moved out of bookdrop/
// on approve — libraries with a fileNamingPattern relocate into the library
// root, so this spec drives the "file is gone on delete" check through the
// API instead of hard-coding a disk path.
async function createBookViaBookdrop(
  adminApi: APIRequestContext,
  label: string,
): Promise<{ book: BookLite; filename: string }> {
  const { filename, cleanup: cleanupDropFile } = await dropFixture(
    'epub',
    label,
  );

  try {
    const deadline = Date.now() + 20_000;
    let dropId: string | undefined;
    while (Date.now() < deadline) {
      const res = await adminApi.get('/api/v1/bookdrop');
      if (res.ok()) {
        const { items } = (await res.json()) as {
          items: { id: string; filename: string; state: string }[];
        };
        const row = items.find((i) => i.filename === filename);
        if (row?.state === 'ready') {
          dropId = row.id;
          break;
        }
      }
      await new Promise((r) => setTimeout(r, 500));
    }
    if (!dropId) {
      throw new Error(
        `EPUB "${filename}" did not reach 'ready' state in time`,
      );
    }

    const approve = await adminApi.post(`/api/v1/bookdrop/${dropId}/approve`);
    expect(approve.ok()).toBeTruthy();
    const { book } = (await approve.json()) as { book: BookLite };
    return { book, filename };
  } catch (err) {
    // If we never got to approve, the source file is still on disk and
    // will otherwise leak. Approved books own their path, so the caller
    // (or the delete handler under test) is responsible for cleanup.
    await cleanupDropFile();
    throw err;
  }
}

test.describe('book delete', () => {
  test('admin deletes a book: UI redirects, API 404s, file fetch 404s', async ({
    page,
    adminApi,
  }) => {
    // Full BookDrop → approve → delete cycle. The upstream watcher ticks at
    // 5s and EPUB extraction adds a second or two, so give the whole spec
    // comfortable headroom.
    test.setTimeout(60_000);

    const { book } = await createBookViaBookdrop(adminApi, 'delete');

    // Sanity: the approved book's file is servable before we delete.
    const fileBefore = await adminApi.get(`/api/v1/books/${book.id}/file`);
    expect(fileBefore.ok()).toBeTruthy();

    await page.goto(`/book/${book.id}`);
    await expect(page.getByRole('heading', { name: book.title })).toBeVisible();

    // Danger zone lives in the Versions tab — shadcn TabsTrigger uses
    // role="tab" and the label is capitalized.
    await page.getByRole('tab', { name: 'Versions' }).click();
    await expect(page.getByText('Danger zone', { exact: true })).toBeVisible();

    // The destructive action opens a shadcn Dialog that requires the
    // user to retype the title — this replaced window.confirm.
    await page.getByRole('button', { name: /Delete book/ }).click();
    const dialog = page.getByRole('dialog');
    await expect(
      dialog.getByRole('heading', { name: 'Delete book' }),
    ).toBeVisible();
    await dialog.getByLabel('Type the title to confirm.').fill(book.title);
    await dialog.getByRole('button', { name: /Delete book/ }).click();

    // On success the page invalidates caches and navigates to /library.
    await expect(page).toHaveURL(/\/library$/, { timeout: 10_000 });

    // DB row is gone — GET returns 404.
    const after = await adminApi.get(`/api/v1/books/${book.id}`);
    expect(after.status()).toBe(404);

    // The file endpoint 404s too — the delete handler unlinks the source
    // best-effort after the DB write.
    const fileAfter = await adminApi.get(`/api/v1/books/${book.id}/file`);
    expect(fileAfter.status()).toBe(404);
  });

  test('DELETE /books/:id on a missing id returns 404', async ({ adminApi }) => {
    // A plausibly-shaped-but-unknown UUID. The handler does a lookup first
    // and returns 404 before touching the repo delete.
    const res = await adminApi.delete(
      '/api/v1/books/00000000-0000-0000-0000-000000000000',
    );
    expect(res.status()).toBe(404);
  });
});
