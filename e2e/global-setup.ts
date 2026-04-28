import { request, type APIRequestContext } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';

import {
  ADMIN_EMAIL,
  ADMIN_PASSWORD,
  ADMIN_STATE_PATH,
  BASE_URL,
} from './fixtures/constants';

// Logs in once as the seeded admin and persists the session cookie to
// ADMIN_STATE_PATH. Authenticated specs reuse it via `test.use({ storageState })`
// so they don't re-do the login dance in every test.
//
// On a fresh database (e.g. the SQLite e2e lane that doesn't load
// scripts/seed.sql) the first login returns 401. We fall back to the
// public /auth/signup endpoint to bootstrap the admin, then retry login.
// The seeded PG path takes the happy first-login branch.
export default async function globalSetup() {
  // The Go backend's CSRFGuard rejects state-changing requests without a
  // matching Origin/Referer header. Browser-driven requests set it
  // automatically; APIRequestContext doesn't, so we pass it explicitly.
  const ctx = await request.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: { Origin: BASE_URL },
  });

  let res = await login(ctx);
  if (res.status === 401) {
    await signup(ctx);
    res = await login(ctx);
  }
  if (!res.ok) {
    throw new Error(
      `globalSetup: login failed (${res.status}) against ${BASE_URL}.\n` +
        `Make sure the Go binary is running (\`make build && ./tmp/embookshelf\`).\n` +
        `${res.body}`,
    );
  }

  const statePath = resolve(ADMIN_STATE_PATH);
  await mkdir(dirname(statePath), { recursive: true });
  await ctx.storageState({ path: statePath });
  await ctx.dispose();
}

async function login(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  return { ok: r.ok(), status: r.status(), body: await r.text() };
}

async function signup(ctx: APIRequestContext) {
  const r = await ctx.post('/api/v1/auth/signup', {
    data: { email: ADMIN_EMAIL, name: 'Admin', password: ADMIN_PASSWORD },
  });
  if (!r.ok()) {
    throw new Error(
      `globalSetup: signup failed (${r.status()}) against ${BASE_URL}.\n` +
        `${await r.text()}`,
    );
  }
}
