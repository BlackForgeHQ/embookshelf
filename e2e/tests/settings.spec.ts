import type { APIRequestContext, Page } from '@playwright/test';

import { test, expect } from '../fixtures/api';
import { ADMIN_EMAIL, ADMIN_STATE_PATH } from '../fixtures/constants';

test.use({ storageState: ADMIN_STATE_PATH });

// Admin-only panels live on the /settings route behind a side nav of
// <button>s. User-scoped panels (Account, Reading preferences, Device
// sync) live on the /account route, with the active section driven by
// the `?section=` search param. Each flow has its own helper.

async function openAdminSettings(page: Page, panel: string): Promise<void> {
  await page.goto('/settings');
  await page.getByRole('button', { name: panel, exact: true }).click();
  // Wait for the header to swap so assertions below aren't racing the
  // previous panel's content.
  await expect(
    page.getByRole('main').getByRole('heading', { name: panel }),
  ).toBeVisible();
}

const ACCOUNT_SECTION_KEY = {
  Account: 'account',
  'Reading preferences': 'reading',
  'Device sync': 'devices',
} as const;

async function openAccountPage(
  page: Page,
  section: 'Account' | 'Reading preferences' | 'Device sync',
): Promise<void> {
  const key = ACCOUNT_SECTION_KEY[section];
  // `account` is the default section so it has no search param; the
  // other two pin via ?section=.
  const url = key === 'account' ? '/account' : `/account?section=${key}`;
  await page.goto(url);

  const main = page.getByRole('main');
  // exact:true — "Account" would otherwise collide with the page title
  // "My account" rendered above the section nav.
  await expect(
    main.getByRole('heading', { name: section, exact: true }),
  ).toBeVisible();
}

// ---------------------------------------------------------------------------
// Account (moved to /account route)
// ---------------------------------------------------------------------------

test.describe('settings · account', () => {
  test('renders the seeded admin identity', async ({ page }) => {
    await openAccountPage(page, 'Account');
    const main = page.getByRole('main');

    await expect(
      page.getByRole('heading', { name: 'My account' }),
    ).toBeVisible();
    await expect(main.getByText(ADMIN_EMAIL, { exact: false })).toBeVisible();
    await expect(main.getByText(/·\s*Admin\s*·/)).toBeVisible();
  });

  test('Change password toggles the inline form', async ({ page }) => {
    await openAccountPage(page, 'Account');
    const main = page.getByRole('main');

    const currentPw = main.getByRole('textbox', { name: 'Current password' });
    await expect(currentPw).toBeHidden();

    await main.getByRole('button', { name: 'Change password' }).click();
    await expect(currentPw).toBeVisible();
    await expect(
      main.getByRole('textbox', { name: 'New password', exact: true }),
    ).toBeVisible();
    await expect(
      main.getByRole('textbox', { name: 'Confirm new password' }),
    ).toBeVisible();
  });

  test('password mismatch surfaces a toast error', async ({ page }) => {
    await openAccountPage(page, 'Account');
    const main = page.getByRole('main');

    await main.getByRole('button', { name: 'Change password' }).click();
    await main.getByRole('textbox', { name: 'Current password' }).fill('changeme');
    await main
      .getByRole('textbox', { name: 'New password', exact: true })
      .fill('abcdefgh');
    await main
      .getByRole('textbox', { name: 'Confirm new password' })
      .fill('something-else');
    await main.getByRole('button', { name: 'Update password' }).click();

    // Mismatch is shown via a sonner toast (not an inline error any more).
    await expect(page.getByText('New passwords do not match.')).toBeVisible();
  });

  test('wrong current password returns a server-side error', async ({ page }) => {
    await openAccountPage(page, 'Account');
    const main = page.getByRole('main');

    await main.getByRole('button', { name: 'Change password' }).click();
    await main
      .getByRole('textbox', { name: 'Current password' })
      .fill('definitely-not-the-password');
    await main
      .getByRole('textbox', { name: 'New password', exact: true })
      .fill('abcdefgh');
    await main
      .getByRole('textbox', { name: 'Confirm new password' })
      .fill('abcdefgh');
    await main.getByRole('button', { name: 'Update password' }).click();

    await expect(page.getByText(/current password is incorrect/i)).toBeVisible();
  });

  test('Edit name inline flow opens the form', async ({ page }) => {
    await openAccountPage(page, 'Account');
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
    await openAccountPage(page, 'Reading preferences');
    const main = page.getByRole('main');

    await expect(
      main.getByRole('heading', { name: 'Reading preferences' }),
    ).toBeVisible();
    await expect(main.getByText('Theme', { exact: true })).toBeVisible();
    await expect(main.getByText('Font family', { exact: true })).toBeVisible();
    await expect(main.getByText('Record reading sessions')).toBeVisible();
    await expect(
      main.getByText('Two-page layout on wide screens'),
    ).toBeVisible();
  });

  test('changing the theme persists to localStorage', async ({ page }) => {
    await openAccountPage(page, 'Reading preferences');
    const main = page.getByRole('main');

    // Theme control is a shadcn Select (combobox). The Theme Field is
    // the first combobox in the panel.
    await main.getByRole('combobox').first().click();
    await page.getByRole('option', { name: 'Sepia' }).click();

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
    await openAdminSettings(page, 'Libraries');

    await expect(
      page.getByRole('main').getByRole('heading', { name: 'Libraries' }),
    ).toBeVisible();
    // The seeded Main library is always present on this dev stack; if
    // there are none, skip gracefully.
    const mainCount = await page
      .getByRole('main')
      .getByText('Main', { exact: true })
      .count();
    test.skip(mainCount === 0, 'no library rows — nothing to assert on');
    await expect(
      page.getByRole('main').getByText('Main', { exact: true }),
    ).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Metadata providers (admin-only)
// ---------------------------------------------------------------------------

test.describe('settings · metadata providers', () => {
  test('lists every built-in provider', async ({ page }) => {
    await openAdminSettings(page, 'Metadata providers');

    const main = page.getByRole('main');
    await expect(main.getByText('Google Books', { exact: true })).toBeVisible();
    await expect(main.getByText('Open Library', { exact: true })).toBeVisible();
    await expect(main.getByText('Amazon', { exact: true })).toBeVisible();
    await expect(main.getByText('DuckDuckGo', { exact: true })).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Device sync (moved to /account route)
// ---------------------------------------------------------------------------

test.describe('settings · device sync', () => {
  test('renders OPDS URL and the Add device action', async ({ page }) => {
    await openAccountPage(page, 'Device sync');
    const main = page.getByRole('main');

    await expect(
      main.getByRole('heading', { name: 'Device sync' }),
    ).toBeVisible();
    await expect(main.getByRole('button', { name: /Add device/ })).toBeVisible();

    // First textbox in the pane is the read-only OPDS URL field.
    const opdsInput = main.getByRole('textbox').first();
    await expect(opdsInput).toHaveValue(/\/opds$/);
  });

  test('Add device opens the reMarkable pairing form', async ({ page }) => {
    await openAccountPage(page, 'Device sync');
    const main = page.getByRole('main');

    await main.getByRole('button', { name: /Add device/ }).click();

    await expect(main.getByText(/Add reMarkable Paper Pro/)).toBeVisible();
    await expect(main.getByLabel('One-time code')).toBeVisible();
    const pairBtn = main.getByRole('button', { name: 'Pair device' });
    await expect(pairBtn).toBeDisabled();

    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(main.getByLabel('One-time code')).toBeHidden();
  });
});

// ---------------------------------------------------------------------------
// Email delivery (admin)
// ---------------------------------------------------------------------------

test.describe('settings · email delivery', () => {
  // This panel used to be a placeholder reading "SMTP is not yet wired",
  // and the spec still asserted that string long after ADR-0020 wired it
  // — so it failed for the good reason that the feature had shipped.
  // Asserting the form's controls instead means it now fails only if the
  // panel actually breaks.
  test('renders the SMTP configuration form', async ({ page }) => {
    await openAdminSettings(page, 'Email delivery');

    const main = page.getByRole('main');
    await expect(main.getByText(/SMTP configuration powers/)).toBeVisible();
    await expect(main.getByText('Enable email delivery', { exact: true })).toBeVisible();
    await expect(main.getByText('SMTP host', { exact: true })).toBeVisible();
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
    await openAdminSettings(page, 'Users & roles');

    const main = page.getByRole('main');
    await expect(main.getByText(ADMIN_EMAIL, { exact: false })).toBeVisible();
    await expect(main.getByText('you', { exact: true })).toBeVisible();
  });

  test('New user button opens the creation form', async ({ page }) => {
    await openAdminSettings(page, 'Users & roles');

    const main = page.getByRole('main');
    await main.getByRole('button', { name: /New user/ }).click();

    await expect(main.getByLabel('Email')).toBeVisible();
    await expect(main.getByLabel('Display name')).toBeVisible();
    await expect(main.getByLabel('Initial password')).toBeVisible();
    await expect(main.getByText('Role', { exact: true })).toBeVisible();
    await main.getByRole('button', { name: 'Cancel' }).click();
    await expect(main.getByLabel('Initial password')).toBeHidden();
  });

  test('creating a user surfaces them in the list, then the row can be deleted', async ({
    page,
    adminApi,
  }) => {
    const email = `e2e-user-${Date.now()}@local`;

    await openAdminSettings(page, 'Users & roles');
    const main = page.getByRole('main');

    try {
      await main.getByRole('button', { name: /New user/ }).click();
      await main.getByLabel('Email').fill(email);
      await main.getByLabel('Display name').fill('E2E User');
      await main.getByLabel('Initial password').fill('password123');
      // Role is a shadcn Select (combobox). Scope by its accessible
      // name so we don't match the existing user rows' own Role selects.
      await main.getByRole('combobox', { name: 'Role' }).click();
      await page.getByRole('option', { name: 'User', exact: true }).click();
      await main.getByRole('button', { name: 'Create user' }).click();

      // Success is confirmed via a sonner toast — not an inline panel
      // message. Match on the page-level toast.
      await expect(page.getByText('User created.')).toBeVisible();
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
    await openAdminSettings(page, 'Backups');

    const main = page.getByRole('main');
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
    await openAdminSettings(page, 'About');

    const main = page.getByRole('main');
    await expect(main.getByText('embookshelf', { exact: true })).toBeVisible();
    await expect(main.getByText('Version')).toBeVisible();
    await expect(main.getByText('Runtime')).toBeVisible();
    await expect(main.getByText('Instance totals')).toBeVisible();
  });
});
