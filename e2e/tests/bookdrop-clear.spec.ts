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

// Clearing processed history lives in Settings → BookDrop, not on the
// /bookdrop queue page. It moved with the redesign and these specs kept
// driving the old page, where neither the Clear button nor the processed
// rows exist any more (#216).
async function openBookDropSettings(page: import('@playwright/test').Page) {
  await page.goto('/settings');
  await page.getByRole('button', { name: 'BookDrop' }).click();
  await expect(
    page.getByRole('heading', { name: 'Recently processed' }),
  ).toBeVisible();
}

// The trigger is gated on there being processed items, so its enabled
// state is the precondition these specs used to check by looking for a
// filename in a list. The panel headlines rows by title, not filename.
const clearTrigger = (page: import('@playwright/test').Page) =>
  page.getByRole('button', { name: 'Clear processed history' });

test.describe('bookdrop · clear processed dialog', () => {
  test('Clear button opens a confirm dialog; Cancel leaves the finished list intact', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const { filename, cleanup } = await seedRejectedItem(adminApi, 'clear-cancel');

    try {
      await openBookDropSettings(page);

      const clearBtn = clearTrigger(page);
      await expect(clearBtn).toBeEnabled({ timeout: 10_000 });
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

      // Still processed items to clear — Cancel must not hit the API.
      await expect(clearTrigger(page)).toBeEnabled();
    } finally {
      await cleanup();
    }
  });

  test('confirming Clear removes the processed rows and fires DELETE /settings/bookdrop/processed', async ({
    page,
    adminApi,
  }) => {
    test.setTimeout(45_000);

    const { filename, cleanup } = await seedRejectedItem(adminApi, 'clear-confirm');

    try {
      await openBookDropSettings(page);
      await expect(clearTrigger(page)).toBeEnabled({ timeout: 10_000 });
      await clearTrigger(page).click();
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();

      const deleteCall = page.waitForResponse(
        (r) =>
          r.request().method() === 'DELETE' &&
          // The endpoint moved under /settings with the feature.
          r.url().endsWith('/api/v1/settings/bookdrop/processed') &&
          r.ok(),
      );
      await dialog.getByRole('button', { name: /^Clear$/ }).click();
      await deleteCall;

      await expect(dialog).toBeHidden();
      // Nothing processed left, so the trigger gates itself off again.
      await expect(clearTrigger(page)).toBeDisabled();

      // And the API confirms it's gone too.
      const res = await adminApi.get('/api/v1/bookdrop');
      const { items } = (await res.json()) as { items: BookDropItem[] };
      expect(items.find((i) => i.filename === filename)).toBeUndefined();
    } finally {
      await cleanup();
    }
  });
});
