import { defineConfig, devices } from '@playwright/test';

import { BASE_URL } from './fixtures/constants';

// Phase 1 config: single project against the Go binary on :6060 — which
// serves the built SPA + API from the same origin. Run `make build &&
// ./tmp/embookshelf` (and `make db-up && make seed`) before invoking tests.
// Phase 2 will add a Vite-dev project once the /api proxy is untangled.
export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? [['github'], ['html']] : 'list',
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'dev',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
