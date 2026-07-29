import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

type ProviderInfo = {
  id: string;
  name: string;
  enabled: boolean;
  priority?: number;
  schema?: Array<{ key: string; label: string; kind: string }>;
  config?: Record<string, unknown>;
};

async function fetchMetadata(api: APIRequestContext): Promise<{ autoEnrich: boolean }> {
  const res = await api.get('/api/v1/settings/metadata');
  expect(res.ok()).toBeTruthy();
  return (await res.json()) as { autoEnrich: boolean };
}

async function fetchProviders(api: APIRequestContext): Promise<ProviderInfo[]> {
  const res = await api.get('/api/v1/settings/providers');
  expect(res.ok()).toBeTruthy();
  const { providers } = (await res.json()) as { providers: ProviderInfo[] };
  return providers;
}

async function openProvidersPanel(
  page: import('@playwright/test').Page,
): Promise<void> {
  await page.goto('/settings');
  await page
    .getByRole('button', { name: 'Metadata providers', exact: true })
    .click();
  await expect(
    page
      .getByRole('main')
      .getByRole('heading', { name: 'Metadata providers' }),
  ).toBeVisible();
}

test.describe('settings · metadata (auto-enrich + provider rows)', () => {
  test('auto-enrich switch toggles the instance setting and restores', async ({
    page,
    adminApi,
  }) => {
    const original = await fetchMetadata(adminApi);

    try {
      // Watch for the panel's own GET so we know the switch has had a
      // chance to reflect the persisted value — the underlying Switch
      // mounts with a default before TanStack Query fills it in.
      const settingsLoaded = page.waitForResponse(
        (r) =>
          r.request().method() === 'GET' &&
          r.url().endsWith('/api/v1/settings/metadata') &&
          r.ok(),
      );
      await openProvidersPanel(page);
      await settingsLoaded;

      const switchEl = page.getByRole('switch', {
        name: /Toggle auto-enrich/,
      });
      await expect(switchEl).toBeVisible();
      // Wait for the switch to settle on the persisted value before
      // reading it, otherwise we race the post-fetch re-render.
      await expect(switchEl).toHaveAttribute(
        'aria-checked',
        String(original.autoEnrich),
      );

      const wasChecked = (await switchEl.getAttribute('aria-checked')) === 'true';
      expect(wasChecked).toBe(original.autoEnrich);

      const putCall = page.waitForResponse(
        (r) =>
          r.request().method() === 'PUT' &&
          r.url().endsWith('/api/v1/settings/metadata') &&
          r.ok(),
      );
      await switchEl.click();
      await putCall;

      const after = await fetchMetadata(adminApi);
      expect(after.autoEnrich).toBe(!original.autoEnrich);
    } finally {
      // Always restore the seeded state.
      await adminApi.put('/api/v1/settings/metadata', {
        data: { autoEnrich: original.autoEnrich },
      });
    }
  });

  test('provider rows expose Move up/down buttons with correct disabled edges', async ({
    page,
    adminApi,
  }) => {
    const providers = await fetchProviders(adminApi);
    expect(providers.length).toBeGreaterThan(1);

    await openProvidersPanel(page);

    const upButtons = page.getByRole('button', { name: 'Move up' });
    const downButtons = page.getByRole('button', { name: 'Move down' });

    // One pair per provider row.
    await expect(upButtons).toHaveCount(providers.length);
    await expect(downButtons).toHaveCount(providers.length);

    // The top row can't move up, the bottom can't move down.
    await expect(upButtons.first()).toBeDisabled();
    await expect(downButtons.last()).toBeDisabled();
  });

  test('Google Books row renders API key + Language fields from its config schema', async ({
    page,
    adminApi,
  }) => {
    const providers = await fetchProviders(adminApi);
    const gb = providers.find((p) => p.id === 'google_books');
    test.skip(!gb, 'google_books provider not present in this build');

    await openProvidersPanel(page);
    const main = page.getByRole('main');

    await expect(main.getByText('Google Books', { exact: true })).toBeVisible();

    // The schema-driven fields live behind a per-provider "Config"
    // toggle and start collapsed. This spec asserted them on a closed
    // row, so it could only ever have passed while the rows rendered
    // their config inline (#216). aria-controls names the region, which
    // makes both the toggle and the panel addressable without guessing
    // at row nesting.
    await main.locator('button[aria-controls="provider-config-google_books"]').click();
    const config = main.locator('#provider-config-google_books');

    // Labels are rendered as <span>s (no htmlFor binding), so we match
    // the visible label text and the input by its placeholder.
    await expect(config.getByText('API key', { exact: true })).toBeVisible();
    await expect(config.getByText('Language', { exact: true })).toBeVisible();
    // Google Books' apiKey field has the "AIza…" placeholder.
    await expect(config.locator('input[placeholder^="AIza"]')).toBeVisible();
  });
});
