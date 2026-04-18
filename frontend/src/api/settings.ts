import { api } from './client';
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
