import { test, expect } from '@playwright/test';

import { ADMIN_EMAIL, ADMIN_PASSWORD } from '../fixtures/constants';

// These specs run without the cached admin session so we can exercise the
// login flow itself.
test.use({ storageState: { cookies: [], origins: [] } });

test.describe('auth', () => {
  test('seeded admin can sign in and lands on the dashboard', async ({ page }) => {
    await page.goto('/login');
    await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();

    await page.getByLabel('Email').fill(ADMIN_EMAIL);
    await page.getByLabel('Password').fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(page).toHaveURL('/');
  });

  test('wrong password keeps the user on /login with an error flash', async ({ page }) => {
    await page.goto('/login');

    await page.getByLabel('Email').fill(ADMIN_EMAIL);
    await page.getByLabel('Password').fill('definitely-not-the-password');
    await page.getByRole('button', { name: 'Sign in' }).click();

    await expect(page.locator('.flash.error')).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });

  test('unauthenticated access to /library redirects to /login', async ({ page }) => {
    await page.goto('/library');
    await expect(page).toHaveURL(/\/login/);
  });

  test('logout invalidates the session cookie', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Email').fill(ADMIN_EMAIL);
    await page.getByLabel('Password').fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page).toHaveURL('/');

    // Issue the POST from inside the page so the browser sets Origin —
    // the Go backend's CSRFGuard rejects state-changing requests without it.
    const logoutStatus = await page.evaluate(async () => {
      const r = await fetch('/api/v1/auth/logout', {
        method: 'POST',
        credentials: 'include',
      });
      return r.status;
    });
    expect(logoutStatus).toBe(204);

    const meStatus = await page.evaluate(async () => {
      const r = await fetch('/api/v1/me', { credentials: 'include' });
      return r.status;
    });
    expect(meStatus).toBe(401);
  });
});
