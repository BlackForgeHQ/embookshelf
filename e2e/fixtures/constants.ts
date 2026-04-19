// Tests target the Go binary (which serves both SPA + API from the same
// origin). The Vite dev server on :5173 currently has a broken /api proxy
// — the TanStack Start plugin intercepts /api/* before Vite's proxy runs —
// so driving tests through :5173 doesn't work until that's untangled.
export const BASE_URL = process.env.BASE_URL ?? 'http://localhost:6060';
export const ADMIN_EMAIL = process.env.ADMIN_EMAIL ?? 'admin@local';
export const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD ?? 'changeme';

// Relative to the e2e/ directory (Playwright resolves paths from the config
// location). Kept out of git via .gitignore.
export const ADMIN_STATE_PATH = '.auth/admin.json';
