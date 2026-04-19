import { api } from './client';

// Kinds the server ships with drivers for. Keep in sync with
// internal/model/device.go — the Settings UI picks its "Add device" list
// from here.
export type DeviceKind = 'remarkable-paper-pro';

export type Device = {
  id: string;
  kind: DeviceKind;
  name: string;
  lastSentAt?: string;
  lastError?: string;
  createdAt: string;
};

export async function fetchDevices(): Promise<Device[]> {
  const { devices } = await api<{ devices: Device[] }>('/api/v1/devices');
  return devices;
}

// Pairs a new device. For reMarkable Paper Pro, `params` carries the
// 8-character code users see at https://my.remarkable.com/device/desktop/connect.
export async function pairDevice(body: {
  kind: DeviceKind;
  name: string;
  params: Record<string, unknown>;
}): Promise<Device> {
  const { device } = await api<{ device: Device }>('/api/v1/devices', {
    method: 'POST',
    body: JSON.stringify(body),
  });
  return device;
}

export async function deleteDevice(id: string): Promise<void> {
  await api<void>(`/api/v1/devices/${id}`, { method: 'DELETE' });
}

export async function sendBookToDevice(bookId: string, deviceId: string): Promise<void> {
  await api<void>(`/api/v1/books/${bookId}/send/${deviceId}`, { method: 'POST' });
}

export const devicesQueryKey = ['devices'] as const;

// Human-friendly labels for device kinds — used by the Add-device list
// and the badge on each device card.
export const DEVICE_KIND_LABELS: Record<DeviceKind, string> = {
  'remarkable-paper-pro': 'reMarkable Paper Pro',
};
