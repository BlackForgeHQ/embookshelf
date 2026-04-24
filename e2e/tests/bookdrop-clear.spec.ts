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

async function seedRejectedItem(
  adminApi: APIRequestContext,
  label: string,
): Promise<{ filename: string; cleanup: () => Promise<void> }> {
  const { filename, cleanup } = await dropEpub(label);

  // Wait for the watcher to pick up the drop, then reject so the row
  // moves into the "Recently processed" bucket that the Clear button
  // acts on.
  const deadline = Date.now() + 20_000;
  let hit: BookDropItem | undefined;
  while (Date.now() < deadline) {
    const res = await adminApi.get('/api/v1/bookdrop');
    if (res.ok()) {
      const { items } = (await res.json()) as { items: BookDropItem[] };
      hit = items.find((i) => i.filename === filename);
      if (hit && hit.state !== 'discovered') break;
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  if (!hit) throw new Error(`drop ${filename} never surfaced`);

  const rej = await adminApi.post(`/api/v1/bookdrop/${hit.id}/reject`);
  expect(rej.ok()).toBeTruthy();

  return { filename, cleanup };
}

// Run these two specs sequentially — both mutate the same global
// "Recently processed" bucket, and DELETE /bookdrop/processed in one
// spec would race the other's Clear button visibility.
test.describe.configure({ mode: 'serial' });

test.describe('bookdrop · clear processed dialog', () => {
  test('Clear button opens a confirm dialog; Cancel leaves the finished list intact', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const { filename, cleanup } = await seedRejectedItem(adminApi, 'clear-cancel');

    try {
      await page.goto('/bookdrop');

      // Finished list header is scoped by the filename row — wait for it so
      // we know the Clear button (gated on finished.length > 0) is rendered.
      await expect(page.getByText(filename).first()).toBeVisible({
        timeout: 10_000,
      });

      const clearBtn = page.getByRole('button', { name: /^Clear$/ });
      await clearBtn.click();

      // shadcn Dialog renders title + Cancel/Clear buttons in the dialog
      // role. Scope the action buttons to the dialog so they don't collide
      // with the page-level trigger.
      const dialog = page.getByRole('dialog');
      await expect(
        dialog.getByRole('heading', { name: 'Clear processed history?' }),
      ).toBeVisible();
      await expect(dialog.getByText(/Remove \d+ processed/)).toBeVisible();

      await dialog.getByRole('button', { name: 'Cancel' }).click();
      await expect(dialog).toBeHidden();

      // Row should still be there — Cancel must not hit the API.
      await expect(page.getByText(filename).first()).toBeVisible();
    } finally {
      await cleanup();
    }
  });

  test('confirming Clear removes the processed rows and fires DELETE /bookdrop/processed', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const { filename, cleanup } = await seedRejectedItem(adminApi, 'clear-confirm');

    try {
      await page.goto('/bookdrop');
      await expect(page.getByText(filename).first()).toBeVisible({
        timeout: 10_000,
      });

      await page.getByRole('button', { name: /^Clear$/ }).click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();

      const deleteCall = page.waitForResponse(
        (r) =>
          r.request().method() === 'DELETE' &&
          r.url().endsWith('/api/v1/bookdrop/processed') &&
          r.ok(),
      );
      await dialog.getByRole('button', { name: /^Clear$/ }).click();
      await deleteCall;

      await expect(dialog).toBeHidden();
      // After clear, our seeded filename disappears from the queue.
      await expect(page.getByText(filename)).toHaveCount(0);

      // And the API confirms it's gone too.
      const res = await adminApi.get('/api/v1/bookdrop');
      const { items } = (await res.json()) as { items: BookDropItem[] };
      expect(items.find((i) => i.filename === filename)).toBeUndefined();
    } finally {
      await cleanup();
    }
  });
});
