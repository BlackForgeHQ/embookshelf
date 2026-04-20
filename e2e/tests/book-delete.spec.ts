import { access, constants as fsConstants } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { dropFixture } from '../fixtures/bookdrop';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

// Mirrors BOOKDROP_DIR in fixtures/bookdrop.ts — the binary's cwd is the
// repo root, so the drop directory lives one level above e2e/.
const __dirname = dirname(fileURLToPath(import.meta.url));
const BOOKDROP_DIR = resolve(__dirname, '..', '..', 'bookdrop');

type BookLite = { id: string; title: string; format: string };

// createBookViaBookdrop drops an EPUB, waits for it to reach 'ready', and
// approves it. Returns the book + the on-disk path so the test can assert
// the file is unlinked after delete.
async function createBookViaBookdrop(
  adminApi: APIRequestContext,
  label: string,
): Promise<{ book: BookLite; filename: string; diskPath: string }> {
  const { filename, cleanup: cleanupDropFile } = await dropFixture(
    'epub',
    label,
  );
  const diskPath = resolve(BOOKDROP_DIR, filename);

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
    return { book, filename, diskPath };
  } catch (err) {
    // If we never got to approve, the source file is still on disk and
    // will otherwise leak. Approved books own their path, so the caller
    // (or the delete handler under test) is responsible for cleanup.
    await cleanupDropFile();
    throw err;
  }
}

async function fileExists(path: string): Promise<boolean> {
  try {
    await access(path, fsConstants.F_OK);
    return true;
  } catch {
    return false;
  }
}

test.describe('book delete', () => {
  test('admin deletes a book: UI redirects, API 404s, disk file is gone', async ({
    page,
    adminApi,
  }) => {
    // Full BookDrop → approve → delete cycle. The upstream watcher ticks at
    // 5s and EPUB extraction adds a second or two, so give the whole spec
    // comfortable headroom.
    test.setTimeout(60_000);

    const { book, diskPath } = await createBookViaBookdrop(adminApi, 'delete');

    // Sanity: the approved file exists on disk before we delete.
    expect(await fileExists(diskPath)).toBe(true);

    await page.goto(`/book/${book.id}`);
    await expect(page.getByRole('heading', { name: book.title })).toBeVisible();

    // Danger zone lives in the Versions tab.
    await page.getByRole('button', { name: 'versions' }).click();
    await expect(page.getByText('Danger zone', { exact: true })).toBeVisible();

    // window.confirm is the gate in front of the mutation — accept once.
    page.once('dialog', (dialog) => {
      expect(dialog.type()).toBe('confirm');
      void dialog.accept();
    });

    await page.getByRole('button', { name: /Delete book/ }).click();

    // On success the page invalidates caches and navigates to /library.
    await expect(page).toHaveURL(/\/library$/, { timeout: 10_000 });

    // DB row is gone — GET returns 404.
    const after = await adminApi.get(`/api/v1/books/${book.id}`);
    expect(after.status()).toBe(404);

    // Source file is gone from disk (delete handler unlinks it best-effort
    // after the DB write). Give it a short window — the file unlink runs
    // before the 204 response is written, so one access check is enough.
    expect(await fileExists(diskPath)).toBe(false);
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
