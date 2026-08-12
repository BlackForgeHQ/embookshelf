import { useMemo, useState } from "react"

import type { Device, DeviceKind } from "@/api/devices"
import {
  deleteDevice,
  DEVICE_KIND_LABELS,
  devicesQuery,
  pairDevice,
} from "@/api/devices"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { formatDateTime } from "@/lib/format"
import { Icon } from "@/components/Icon"
import { Card, Field } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export function DevicesPanel() {
  const devices = useApiQuery(devicesQuery)

  const [adding, setAdding] = useState<DeviceKind | null>(null)

  const deleteMut = useApiMutation(deleteDevice, {
    successToast: "Device removed.",
  })

  const [copied, setCopied] = useState(false)
  const opdsUrl = useMemo(() => {
    if (typeof window === "undefined") return ""
    return `${window.location.origin}/opds`
  }, [])
  const copy = async () => {
    await navigator.clipboard.writeText(opdsUrl)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }

  return (
    <>
      <div style={{ display: "flex", alignItems: "baseline", marginBottom: 8 }}>
        <h2 className="t-h2 grow">Device sync</h2>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => setAdding("remarkable-paper-pro")}
        >
          <Icon name="plus" size={13} /> Add device
        </Button>
      </div>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Pair a device once; push books from the library with a single click. Any
        OPDS-aware reader can also pull the catalog below directly.
      </p>

      {adding && (
        <AddDeviceForm
          kind={adding}
          onClose={() => setAdding(null)}
          onPaired={() => setAdding(null)}
        />
      )}

      <div className="t-label" style={{ marginBottom: 10 }}>
        Registered devices
      </div>

      {devices.isLoading && (
        <div
          className="t-small"
          style={{ fontStyle: "italic", marginBottom: 16 }}
        >
          Loading devices…
        </div>
      )}

      {devices.data && devices.data.length === 0 && (
        <div
          className="t-small"
          style={{
            fontStyle: "italic",
            padding: "12px 14px",
            border: "1px dashed var(--color-rule-soft)",
            background: "var(--color-paper-2)",
            marginBottom: 24,
          }}
        >
          No devices paired yet. Click "Add device" to register a reMarkable
          Paper Pro.
        </div>
      )}

      <div
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 8,
          marginBottom: 24,
        }}
      >
        {(devices.data ?? []).map((d) => (
          <DeviceRow
            key={d.id}
            device={d}
            onDelete={() => {
              if (window.confirm(`Remove ${d.name}?`)) deleteMut.mutate(d.id)
            }}
            busy={deleteMut.isPending}
          />
        ))}
      </div>

      <div className="t-label" style={{ marginBottom: 10 }}>
        OPDS catalog
      </div>
      <Card>
        <Field label="Catalog URL">
          <div style={{ display: "flex", gap: 8 }}>
            <Input readOnly value={opdsUrl} className="mono flex-1" />
            <Button type="button" variant="outline" onClick={copy}>
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
        </Field>

        <div className="t-small">
          <div style={{ marginBottom: 6 }}>
            <strong>Authentication:</strong> HTTP Basic Auth (account email +
            password).
          </div>
          <div style={{ marginBottom: 6 }}>
            <strong>Search:</strong> OpenSearch at{" "}
            <span className="mono">{opdsUrl}/search</span>
          </div>
          <div>
            <strong>Compatible:</strong> KOReader, Moon+ Reader, FBReader,
            Marvin, …
          </div>
        </div>
      </Card>
    </>
  )
}

function DeviceRow({
  device,
  onDelete,
  busy,
}: {
  device: Device
  onDelete: () => void
  busy: boolean
}) {
  const lastSent = device.lastSentAt
    ? formatDateTime(device.lastSentAt)
    : null
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 14,
        padding: "12px 14px",
        border: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-0)",
      }}
    >
      <span
        style={{
          width: 8,
          height: 8,
          borderRadius: "50%",
          // editorial-accent, not the shadcn --color-accent (which is a
          // paper tint and rendered the error dot invisible)
          background: device.lastError
            ? "var(--color-editorial-accent)"
            : "var(--color-ok)",
        }}
      />
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="t-item-title">{device.name}</div>
        <div className="t-item-sub">
          {DEVICE_KIND_LABELS[device.kind]}
          {lastSent && ` · last sent ${lastSent}`}
          {!lastSent && " · no pushes yet"}
        </div>
        {device.lastError && (
          <div
            className="mono"
            style={{
              fontSize: 11,
              color: "var(--color-accent-ink)",
              marginTop: 4,
            }}
          >
            {device.lastError}
          </div>
        )}
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={onDelete}
        disabled={busy}
        className="text-(--color-accent-ink)"
        aria-label="Remove device"
      >
        <Icon name="close" size={12} />
      </Button>
    </div>
  )
}

function AddDeviceForm({
  kind,
  onClose,
  onPaired,
}: {
  kind: DeviceKind
  onClose: () => void
  onPaired: () => void
}) {
  const [name, setName] = useState(DEVICE_KIND_LABELS[kind])
  const [code, setCode] = useState("")

  const pairMut = useApiMutation(pairDevice, {
    successToast: "Device paired.",
    onSuccess: () => onPaired(),
  })

  return (
    <Card>
      <div style={{ fontSize: 14, fontWeight: 500 }}>
        Add {DEVICE_KIND_LABELS[kind]}
      </div>
      <div className="t-small">
        Visit{" "}
        <a
          href="https://my.remarkable.com/device/desktop/connect"
          target="_blank"
          rel="noreferrer"
          style={{ color: "var(--color-accent-ink)" }}
        >
          my.remarkable.com/device/desktop/connect
        </a>{" "}
        and sign in. Copy the 8-character one-time code and paste it below. The
        code is consumed once; re-pairing later requires a fresh code.
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          pairMut.mutate({
            kind,
            name: name.trim(),
            params: { code: code.trim() },
          })
        }}
        style={{ display: "flex", flexDirection: "column", gap: 10 }}
      >
        <Field label="Display name">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </Field>
        <Field label="One-time code">
          <Input
            className="mono"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            placeholder="abcd1234"
            autoComplete="off"
            spellCheck={false}
            required
          />
        </Field>

        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
          <Button type="button" variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="submit"
            size="sm"
            disabled={pairMut.isPending || !code.trim()}
          >
            {pairMut.isPending ? "Pairing…" : "Pair device"}
          </Button>
        </div>
      </form>
    </Card>
  )
}
