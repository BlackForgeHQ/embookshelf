import { api } from "./client"
import { defineMutation } from "./mutation"

// Kinds the server ships with drivers for. Keep in sync with
// internal/model/device.go — the Settings UI picks its "Add device" list
// from here.
export type DeviceKind = "remarkable-paper-pro"

export type Device = {
  id: string
  kind: DeviceKind
  name: string
  lastSentAt?: string
  lastError?: string
  createdAt: string
}

export async function fetchDevices(): Promise<Array<Device>> {
  const { devices } = await api<{ devices: Array<Device> }>("/api/v1/devices")
  return devices
}

export const devicesQueryKey = ["devices"] as const

// Pairs a new device. For reMarkable Paper Pro, `params` carries the
// 8-character code users see at https://my.remarkable.com/device/desktop/connect.
export const pairDevice = defineMutation({
  fn: async (body: {
    kind: DeviceKind
    name: string
    params: Record<string, unknown>
  }): Promise<Device> => {
    const { device } = await api<{ device: Device }>("/api/v1/devices", {
      method: "POST",
      body: JSON.stringify(body),
    })
    return device
  },
  invalidates: [devicesQueryKey],
})

export const deleteDevice = defineMutation({
  fn: (id: string): Promise<void> =>
    api<void>(`/api/v1/devices/${id}`, { method: "DELETE" }),
  invalidates: [devicesQueryKey],
})

export const sendBookToDevice = defineMutation({
  fn: (args: { bookId: string; deviceId: string }): Promise<void> =>
    api<void>(`/api/v1/books/${args.bookId}/send/${args.deviceId}`, {
      method: "POST",
    }),
  invalidates: [],
})

// Human-friendly labels for device kinds — used by the Add-device list
// and the badge on each device card.
export const DEVICE_KIND_LABELS: Record<DeviceKind, string> = {
  "remarkable-paper-pro": "reMarkable Paper Pro",
}
