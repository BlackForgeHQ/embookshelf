import type { APIRequestContext } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_EMAIL, ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

// Settings is rendered as a single route with a local nav list on the
// left and a content pane on the right. Every panel swap is a client-only
// state change — no URL update — so we drive the tests through role-named
// buttons in the side nav.

async function openSettings(page: import('@playwright/test').Page, panel: string) {
  await page.goto('/settings');
  if (panel !== 'Account') {
    // Account is the default pane; only click for the others.
    await page.getByRole('button', { name: panel, exact: true }).click();
  }
}

// ---------------------------------------------------------------------------
// Account
// ---------------------------------------------------------------------------

test.describe('settings · account', () => {
  test('renders the seeded admin identity by default', async ({ page }) => {
    await openSettings(page, 'Account');

    await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Account' })).toBeVisible();
    await expect(page.getByText(ADMIN_EMAIL, { exact: false })).toBeVisible();
    await expect(page.getByText(/·\s*Admin\s*·/)).toBeVisible();
  });

  test('Change password toggles the inline form', async ({ page }) => {
    await openSettings(page, 'Account');

    const main = page.getByRole('main');
    const currentPw = main.getByRole('textbox', { name: 'Current password' });
    await expect(currentPw).toBeHidden();

    await main.getByRole('button', { name: 'Change password' }).click();
    await expect(currentPw).toBeVisible();
    await expect(main.getByRole('textbox', { name: 'New password', exact: true })).toBeVisible();
    await expect(main.getByRole('textbox', { name: 'Confirm new password' })).toBeVisible();

    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(currentPw).toBeHidden();
  });

  test('password mismatch surfaces a client-side error', async ({ page }) => {
    await openSettings(page, 'Account');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: 'Change password' }).click();
    await main.getByRole('textbox', { name: 'Current password' }).fill('changeme');
    await main.getByRole('textbox', { name: 'New password', exact: true }).fill('abcdefgh');
    await main.getByRole('textbox', { name: 'Confirm new password' }).fill('something-else');
    await main.getByRole('button', { name: 'Update password' }).click();

    await expect(main.getByText('New passwords do not match.')).toBeVisible();
  });

  test('wrong current password returns a server-side error', async ({ page }) => {
    await openSettings(page, 'Account');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: 'Change password' }).click();
    await main.getByRole('textbox', { name: 'Current password' }).fill('definitely-not-the-password');
    await main.getByRole('textbox', { name: 'New password', exact: true }).fill('abcdefgh');
    await main.getByRole('textbox', { name: 'Confirm new password' }).fill('abcdefgh');
    await main.getByRole('button', { name: 'Update password' }).click();

    await expect(
      main.getByText(/current password is incorrect/i),
    ).toBeVisible();
  });

  test('Edit name inline flow opens the form', async ({ page }) => {
    await openSettings(page, 'Account');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: 'Edit name' }).click();
    await expect(main.getByPlaceholder('Display name')).toBeVisible();
    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(main.getByPlaceholder('Display name')).toBeHidden();
  });
});

// ---------------------------------------------------------------------------
// Reading preferences (client-only, localStorage)
// ---------------------------------------------------------------------------

test.describe('settings · reading preferences', () => {
  test('renders the preference controls', async ({ page }) => {
    await openSettings(page, 'Reading preferences');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Reading preferences' }),
    ).toBeVisible();
    await expect(main.getByText('Theme', { exact: true })).toBeVisible();
    await expect(main.getByText('Font family', { exact: true })).toBeVisible();
    await expect(
      main.getByText('Record reading sessions'),
    ).toBeVisible();
    await expect(
      main.getByText('Two-page layout on wide screens'),
    ).toBeVisible();
  });

  test('changing the theme persists to localStorage', async ({ page }) => {
    await openSettings(page, 'Reading preferences');

    const main = page.getByRole('main');
    // Theme is the first select under the "Theme" label.
    await main.getByLabel('Theme').selectOption('sepia');

    const raw = await page.evaluate(() =>
      window.localStorage.getItem('embookshelf.readingPreferences'),
    );
    expect(raw).not.toBeNull();
    const parsed = JSON.parse(raw ?? '{}') as { theme?: string };
    expect(parsed.theme).toBe('sepia');
  });
});

// ---------------------------------------------------------------------------
// Libraries (admin-only)
// ---------------------------------------------------------------------------

test.describe('settings · libraries', () => {
  test('shows the registered library roots', async ({ page }) => {
    await openSettings(page, 'Libraries');

    await expect(page.getByRole('heading', { name: 'Libraries' })).toBeVisible();
    // "Main" also appears as a sidebar link — scope to main to avoid
    // strict-mode matches.
    await expect(
      page.getByRole('main').getByText('Main', { exact: true }),
    ).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Metadata providers (read-only, configured via ENRICHMENT_PROVIDERS env)
// ---------------------------------------------------------------------------

test.describe('settings · metadata providers', () => {
  test('lists every built-in provider', async ({ page }) => {
    await openSettings(page, 'Metadata providers');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Metadata providers' }),
    ).toBeVisible();
    // { exact: true } so "Amazon" (name) doesn't collide with "amazon" (id)
    // rendered right below in the mono badge; same for the other kinds.
    await expect(main.getByText('Google Books', { exact: true })).toBeVisible();
    await expect(main.getByText('Open Library', { exact: true })).toBeVisible();
    await expect(main.getByText('Amazon', { exact: true })).toBeVisible();
    await expect(main.getByText('DuckDuckGo', { exact: true })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Device sync (per-user — available to every role)
// ---------------------------------------------------------------------------

test.describe('settings · device sync', () => {
  test('renders OPDS URL and the Add device action', async ({ page }) => {
    await openSettings(page, 'Device sync');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Device sync' }),
    ).toBeVisible();
    await expect(main.getByRole('button', { name: /Add device/ })).toBeVisible();

    const opdsInput = main.getByRole('textbox').first();
    await expect(opdsInput).toHaveValue(/\/opds$/);
  });

  test('Add device opens the reMarkable pairing form', async ({ page }) => {
    await openSettings(page, 'Device sync');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: /Add device/ }).click();

    await expect(
      main.getByText(/Add reMarkable Paper Pro/),
    ).toBeVisible();
    await expect(main.getByLabel('One-time code')).toBeVisible();
    const pairBtn = main.getByRole('button', { name: 'Pair device' });
    // Empty code → button stays disabled.
    await expect(pairBtn).toBeDisabled();

    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(main.getByLabel('One-time code')).toBeHidden();
  });
});

// ---------------------------------------------------------------------------
// Email delivery (informational)
// ---------------------------------------------------------------------------

test.describe('settings · email delivery', () => {
  test('renders the informational panel', async ({ page }) => {
    await openSettings(page, 'Email delivery');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Email delivery' }),
    ).toBeVisible();
    await expect(main.getByText(/SMTP is not yet wired/)).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Users & roles (admin-only, CRUD)
// ---------------------------------------------------------------------------

async function deleteUserByEmail(api: APIRequestContext, email: string): Promise<void> {
  const res = await api.get('/api/v1/settings/users');
  if (!res.ok()) return;
  const { users } = (await res.json()) as { users: { id: string; email: string }[] };
  const target = users.find((u) => u.email === email);
  if (!target) return;
  await api.delete(`/api/v1/settings/users/${target.id}`);
}

test.describe('settings · users & roles', () => {
  test('lists existing users including the seeded admin', async ({ page }) => {
    await openSettings(page, 'Users & roles');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Users & roles' }),
    ).toBeVisible();
    await expect(main.getByText(ADMIN_EMAIL, { exact: false })).toBeVisible();
    // The signed-in admin is badged "you".
    await expect(main.getByText('you', { exact: true })).toBeVisible();
  });

  test('New user button opens the creation form', async ({ page }) => {
    await openSettings(page, 'Users & roles');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: /New user/ }).click();

    await expect(main.getByLabel('Email')).toBeVisible();
    await expect(main.getByLabel('Display name')).toBeVisible();
    await expect(main.getByLabel('Initial password')).toBeVisible();
    await expect(main.getByLabel('Role')).toBeVisible();
    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(main.getByLabel('Initial password')).toBeHidden();
  });

  test('creating a user surfaces them in the list, then the row can be deleted', async ({
    page,
    adminApi,
  }) => {
    const email = `e2e-user-${Date.now()}@local`;

    await openSettings(page, 'Users & roles');
    const main = page.getByRole('main');

    try {
      await main.getByRole('button', { name: /New user/ }).click();
      await main.getByLabel('Email').fill(email);
      await main.getByLabel('Display name').fill('E2E User');
      await main.getByLabel('Initial password').fill('password123');
      await main.getByLabel('Role').selectOption('user');
      await main.getByRole('button', { name: 'Create user' }).click();

      await expect(main.getByText('User created.')).toBeVisible();
      await expect(main.getByText(email, { exact: false })).toBeVisible();
    } finally {
      await deleteUserByEmail(adminApi, email);
    }
  });
});

// ---------------------------------------------------------------------------
// Backups (informational)
// ---------------------------------------------------------------------------

test.describe('settings · backups', () => {
  test('renders backup guidance', async ({ page }) => {
    await openSettings(page, 'Backups');

    const main = page.getByRole('main');
    await expect(
      main.getByRole('heading', { name: 'Backups' }),
    ).toBeVisible();
    await expect(main.getByText('pg_dump embookshelf')).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// About
// ---------------------------------------------------------------------------

test.describe('settings · about', () => {
  test('shows the product version and admin-only runtime details', async ({
    page,
  }) => {
    await openSettings(page, 'About');

    const main = page.getByRole('main');
    await expect(main.getByRole('heading', { name: 'About' })).toBeVisible();
    // Product name + version field are always rendered.
    await expect(main.getByText('embookshelf', { exact: true })).toBeVisible();
    await expect(main.getByText('Version')).toBeVisible();
    // Admin-only fields — the seeded admin sees them.
    await expect(main.getByText('Runtime')).toBeVisible();
    await expect(main.getByText('Disk mode')).toBeVisible();
    await expect(main.getByText('Instance totals')).toBeVisible();
  });
});
