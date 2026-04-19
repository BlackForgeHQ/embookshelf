import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { dropEpub } from '../fixtures/bookdrop';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

type BookDropItem = {
  id: string;
  filename: string;
  state: string;
};

// Polls the API until an item with the given filename shows up, or times
// out. The watcher runs on a 5 s tick so 15 s gives us two full cycles of
// headroom.
async function waitForBookDropItem(
  adminApi: APIRequestContext,
  filename: string,
  timeoutMs = 15_000,
): Promise<BookDropItem> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const res = await adminApi.get('/api/v1/bookdrop');
    if (res.ok()) {
      const { items } = (await res.json()) as { items: BookDropItem[] };
      const hit = items.find((i) => i.filename === filename);
      if (hit) return hit;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error(
    `BookDrop item "${filename}" did not appear in the queue within ${timeoutMs}ms`,
  );
}

test.describe('bookdrop', () => {
  test('renders page chrome regardless of queue state', async ({ page }) => {
    await page.goto('/bookdrop');

    await expect(page.getByRole('heading', { name: 'BookDrop' })).toBeVisible();
    await expect(
      page.getByText(/Drop files into \/bookdrop/),
    ).toBeVisible();
    await expect(page.getByRole('button', { name: /Rescan/ })).toBeVisible();
    await expect(page.getByText(/In queue · \d+/)).toBeVisible();
  });

  test('rescan button triggers a queue refetch', async ({ page }) => {
    await page.goto('/bookdrop');

    await page.waitForResponse((r) => r.url().endsWith('/api/v1/bookdrop') && r.ok());
    const refetch = page.waitForResponse(
      (r) => r.url().endsWith('/api/v1/bookdrop') && r.ok(),
    );
    await page.getByRole('button', { name: /Rescan/ }).click();
    const res = await refetch;
    expect(res.status()).toBe(200);
  });

  test('detail panel surfaces extracted EPUB metadata for the selected row', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const { filename, cleanup } = await dropEpub('bookdrop-detail');

    try {
      const item = await waitForBookDropItem(adminApi, filename);

      await page.goto('/bookdrop');

      // Click the queue row for our file so the detail panel focuses on it.
      await page.getByText(filename).first().click();

      // Review panel echoes the file path and surfaces extracted metadata
      // in `<input>` fields. The Field component uses defaultValue, so we
      // match on the DOM value attribute via a CSS selector.
      await expect(page.getByText(`bookdrop/${filename}`)).toBeVisible({
        timeout: 10_000,
      });
      await expect(page.locator('input[value="E2E Sample Book"]')).toBeVisible();
      await expect(page.locator('input[value="E2E Author"]')).toBeVisible();
      await expect(page.locator('input[value="EPUB"]')).toBeVisible();

      // Reject via API so state clears when the spec ends.
      const rej = await adminApi.post(`/api/v1/bookdrop/${item.id}/reject`);
      expect(rej.ok()).toBeTruthy();
    } finally {
      await cleanup();
    }
  });

  test('ingest round-trip: drop an EPUB, wait for the queue, reject to clean up', async ({
    page,
    adminApi,
  }) => {
    // Give this run plenty of time — the watcher ticks at 5 s and metadata
    // extraction adds a second or two on top.
    test.setTimeout(45_000);

    const { filename, cleanup } = await dropEpub('bookdrop');

    try {
      const item = await waitForBookDropItem(adminApi, filename);
      expect(item.state).not.toBe('rejected');

      await page.goto('/bookdrop');
      // The filename appears both in the queue list and in the detail
      // panel's `path` field. One `.first()` is enough.
      await expect(page.getByText(filename).first()).toBeVisible({
        timeout: 10_000,
      });

      // Reject via API so we don't leak an 'active' row into the dev DB;
      // the row transitions to 'rejected' and shifts into "Recently
      // processed".
      const rej = await adminApi.post(`/api/v1/bookdrop/${item.id}/reject`);
      expect(rej.ok()).toBeTruthy();
    } finally {
      await cleanup();
    }
  });
});
