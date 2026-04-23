import { api } from './client';
import type { AuthUser } from './auth';
import type { Library } from './books';

// SettingsLibrary mirrors the server's admin shape. Path is inline
// because a library owns exactly one filesystem root, fixed at
// creation.
export type SettingsLibrary = Library & {
  path: string;
  lastScannedAt: string | null;
  fileCount: number;
  discoveredCount: number;
  fileNamingPattern: string | null;
};

export async function fetchSettingsLibraries(): Promise<SettingsLibrary[]> {
  const { libraries } = await api<{ libraries: SettingsLibrary[] }>(
    '/api/v1/settings/libraries',
  );
  return libraries;
}

export async function createLibrary(body: {
  name: string;
  path: string;
  scan?: boolean;
}): Promise<SettingsLibrary> {
  const { library } = await api<{ library: SettingsLibrary }>(
    '/api/v1/settings/libraries',
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
  );
  return library;
}

export async function prescanLibraryPaths(paths: string[]): Promise<number> {
  const { count } = await api<{ count: number }>(
    '/api/v1/settings/libraries/scan',
    {
      method: 'POST',
      body: JSON.stringify({ paths }),
    },
  );
  return count;
}

// deleteLibrary tears down a library and every book/annotation/etc
// that depends on it. Source files on disk are left alone (they live
// under the user-managed root); cover images and DB rows are removed.
export async function deleteLibrary(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/libraries/${id}`, {
    method: 'DELETE',
  });
}

// rescanLibrary enqueues a library.scan job against the library's
// filesystem root. The response is fire-and-forget (202).
export async function rescanLibrary(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/libraries/${id}/rescan`, {
    method: 'POST',
  });
}

// updateLibraryNamingPattern stores or clears the per-library file naming
// pattern. Pass null to clear (library falls back to "keep original
// filename" on bookdrop approval).
export async function updateLibraryNamingPattern(
  id: string,
  pattern: string | null,
): Promise<SettingsLibrary> {
  const { library } = await api<{ library: SettingsLibrary }>(
    `/api/v1/settings/libraries/${id}/file-naming-pattern`,
    {
      method: 'PATCH',
      body: JSON.stringify({ fileNamingPattern: pattern }),
    },
  );
  return library;
}

export type PreviewPatternSample = {
  title?: string;
  subtitle?: string;
  authors?: string[];
  year?: number;
  series?: string;
  seriesIndex?: number;
  language?: string;
  publisher?: string;
  isbn?: string;
  currentFilename?: string;
  extension?: string;
};

export async function previewNamingPattern(
  pattern: string,
  sample?: PreviewPatternSample,
): Promise<string> {
  const { resolved } = await api<{ resolved: string }>(
    '/api/v1/settings/libraries/pattern/preview',
    {
      method: 'POST',
      body: JSON.stringify({ pattern, sample }),
    },
  );
  return resolved;
}

// fetchDefaultNamingPattern returns the instance-wide default pattern
// used as a fallback when a library does not set its own. Empty string
// means "keep the original filename on approval".
export async function fetchDefaultNamingPattern(): Promise<string> {
  const { pattern } = await api<{ pattern: string }>(
    '/api/v1/settings/libraries/pattern/default',
  );
  return pattern;
}

export async function updateDefaultNamingPattern(
  pattern: string,
): Promise<string> {
  const { pattern: saved } = await api<{ pattern: string }>(
    '/api/v1/settings/libraries/pattern/default',
    {
      method: 'PUT',
      body: JSON.stringify({ pattern }),
    },
  );
  return saved;
}

export const defaultNamingPatternQueryKey = [
  'settings',
  'libraries',
  'default-pattern',
] as const;

export const settingsLibrariesQueryKey = ['settings', 'libraries'] as const;

// --- Instance info ---------------------------------------------------------

export type ProviderInfo = {
  id: string;
  name: string;
  enabled: boolean;
  external: boolean;
};

export type InstanceInfo = {
  version: string;
  goVersion: string;
  diskMode: string;
  allowedOrigins: string[];
  bookDropPath: string;
  dataPath: string;
  migrateOnStart: boolean;
  enrichmentProviders: ProviderInfo[];
  counts: { users: number; libraries: number; books: number };
};

export async function fetchInstanceInfo(): Promise<InstanceInfo> {
  return api<InstanceInfo>('/api/v1/settings/instance');
}

export const instanceInfoQueryKey = ['settings', 'instance'] as const;

// --- Metadata providers (admin) --------------------------------------------

export async function fetchProviderSettings(): Promise<ProviderInfo[]> {
  const { providers } = await api<{ providers: ProviderInfo[] }>(
    '/api/v1/settings/providers',
  );
  return providers;
}

export async function updateProviderSetting(
  id: string,
  enabled: boolean,
): Promise<ProviderInfo[]> {
  const { providers } = await api<{ providers: ProviderInfo[] }>(
    `/api/v1/settings/providers/${encodeURIComponent(id)}`,
    {
      method: 'PATCH',
      body: JSON.stringify({ enabled }),
    },
  );
  return providers;
}

export const providerSettingsQueryKey = ['settings', 'providers'] as const;

// Lightweight, non-admin-gated version of InstanceInfo. Rendered in the
// status bar at the bottom of every page, so all signed-in users can call
// it — mirrors /api/v1/instance on the server.
export type InstanceSummary = {
  version: string;
  diskMode: string;
  libraries: number;
  books: number;
};

export async function fetchInstanceSummary(): Promise<InstanceSummary> {
  return api<InstanceSummary>('/api/v1/instance');
}

export const instanceSummaryQueryKey = ['instance', 'summary'] as const;

// --- Users (admin) ---------------------------------------------------------

export async function fetchSettingsUsers(): Promise<AuthUser[]> {
  const { users } = await api<{ users: AuthUser[] }>('/api/v1/settings/users');
  return users;
}

export async function createSettingsUser(body: {
  email: string;
  name: string;
  password: string;
  role: 'admin' | 'user';
}): Promise<AuthUser> {
  const { user } = await api<{ user: AuthUser }>('/api/v1/settings/users', {
    method: 'POST',
    body: JSON.stringify(body),
  });
  return user;
}

export async function updateSettingsUserRole(
  id: string,
  role: 'admin' | 'user',
): Promise<void> {
  await api<void>(`/api/v1/settings/users/${id}/role`, {
    method: 'PATCH',
    body: JSON.stringify({ role }),
  });
}

export async function deleteSettingsUser(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/users/${id}`, { method: 'DELETE' });
}

export const settingsUsersQueryKey = ['settings', 'users'] as const;
