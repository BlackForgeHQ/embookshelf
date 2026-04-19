import { api } from './client';
import type { AuthUser } from './auth';
import type { Library } from './books';

export type SettingsLibraryPath = {
  id: string;
  libraryId: string;
  path: string;
  lastScannedAt: string | null;
  fileCount: number;
  discoveredCount: number;
  createdAt: string;
};

export type SettingsLibrary = Library & {
  paths: SettingsLibraryPath[];
};

export async function fetchSettingsLibraries(): Promise<SettingsLibrary[]> {
  const { libraries } = await api<{ libraries: SettingsLibrary[] }>(
    '/api/v1/settings/libraries',
  );
  return libraries;
}

export async function createLibraryPath(
  libraryId: string,
  path: string,
): Promise<SettingsLibraryPath> {
  const { path: created } = await api<{ path: SettingsLibraryPath }>(
    '/api/v1/settings/libraries/paths',
    {
      method: 'POST',
      body: JSON.stringify({ libraryId, path }),
    },
  );
  return created;
}

export async function deleteLibraryPath(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/libraries/paths/${id}`, {
    method: 'DELETE',
  });
}

export async function scanLibraryPath(id: string): Promise<void> {
  await api<void>(`/api/v1/settings/libraries/paths/${id}/scan`, {
    method: 'POST',
  });
}

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
