import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"

import type {
  AudiobookEngine,
  AudiobookSettings,
  AudiobookTestResult,
} from "@/api/audiobooks"
import {
  audiobookSettingsQueryKey,
  audiobookVoicesQueryKey,
  fetchAudiobookSettings,
  fetchAudiobookVoices,
  saveAudiobookSettings,
  testAudiobook,
} from "@/api/audiobooks"
import { useApiMutation } from "@/api/mutation"
import { AdminGate, Card, Field, Select, Toggle } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

const emptyForm: AudiobookSettings = { enabled: false, engine: "openai", engines: [] }

// What each engine is actually for, in the terms that decide the choice:
// what it costs and what it gets you. The catalog carries the mechanics
// (caps, whether it needs a model); this is the editorial half.
const ENGINE_NOTES: Record<string, string> = {
  openai:
    "Point this at OpenAI, or at a local Kokoro or openedai-speech — same API either way. Local means free and nothing leaves this machine.",
  elevenlabs:
    "The best narration money buys, and roughly twelve times the price of the others. Voices come from your account.",
  azure:
    "Cheap and capable. The base URL is your region host, e.g. https://westeurope.tts.speech.microsoft.com",
}

export function AudiobooksPanel({ isAdmin }: { isAdmin: boolean }) {
  const settings = useQuery({
    queryKey: audiobookSettingsQueryKey,
    queryFn: fetchAudiobookSettings,
    enabled: isAdmin,
  })

  const [form, setForm] = useState<AudiobookSettings>(emptyForm)
  // Keys are write-only: the server sends keySet instead of the value, so
  // the draft holds only what the admin typed this session. An untouched
  // field submits empty, which the server reads as "keep the stored key".
  const [keyDrafts, setKeyDrafts] = useState<Record<string, string>>({})
  const [testResult, setTestResult] = useState<AudiobookTestResult | null>(null)

  useEffect(() => {
    if (settings.data) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(settings.data)
      setKeyDrafts({})
    }
  }, [settings.data])

  const saveMut = useApiMutation(saveAudiobookSettings, {
    successToast: "Audiobook settings saved.",
    onSuccess: () => setKeyDrafts({}),
  })
  const testMut = useApiMutation(testAudiobook, {
    onSuccess: (res) => setTestResult(res),
    errorToast: (err) => {
      setTestResult(null)
      return err.message
    },
  })

  if (!isAdmin) return <AdminGate label="Audiobook narration" />
  if (settings.isLoading) return <p className="t-small">Loading…</p>

  const selected = form.engines.find((e) => e.id === form.engine)

  function updateEngine(id: string, patch: Partial<AudiobookEngine>) {
    setForm((f) => ({
      ...f,
      engines: f.engines.map((e) => (e.id === id ? { ...e, ...patch } : e)),
    }))
  }

  function submit() {
    saveMut.mutate({
      ...form,
      engines: form.engines.map((e) => ({
        ...e,
        apiKey: keyDrafts[e.id]?.trim() ? keyDrafts[e.id] : "",
      })),
    })
  }

  return (
    <>
      <Card>
        <h3 className="t-h3 mb-2">Audiobook narration</h3>
        <p className="t-small mb-4">
          Reads an EPUB aloud with a text-to-speech engine and saves the
          result beside the book as an MP3 with chapter marks. Generation is
          admin-only and always per book — there is no bulk run, because
          narrating a thousand-book library would cost thousands of dollars.
        </p>

        <Toggle
          label="Enable narration"
          checked={form.enabled}
          onChange={(v) => setForm({ ...form, enabled: v })}
        />

        <div className="mt-4">
          <Field label="Engine">
            <Select
              value={form.engine}
              onChange={(v) => setForm({ ...form, engine: v })}
              options={form.engines.map((e) => ({ value: e.id, label: e.label }))}
            />
          </Field>
          <p className="t-small" style={{ marginTop: 4 }}>
            One engine narrates a book, start to finish. Switching engines
            changes what future books use, not one already in progress.
          </p>
        </div>
      </Card>

      {form.engines.map((engine) => (
        <EngineCard
          key={engine.id}
          engine={engine}
          isSelected={engine.id === form.engine}
          keyDraft={keyDrafts[engine.id] ?? ""}
          onKeyDraft={(v) => setKeyDrafts((d) => ({ ...d, [engine.id]: v }))}
          onChange={(patch) => updateEngine(engine.id, patch)}
        />
      ))}

      <Card className="mt-6">
        <div className="flex items-center gap-2">
          <Button disabled={saveMut.isPending} onClick={submit}>
            Save
          </Button>
          <Button
            variant="outline"
            disabled={testMut.isPending || !selected?.enabled}
            onClick={() => testMut.mutate(undefined)}
            title="Synthesizes one short phrase with the selected engine"
          >
            {testMut.isPending ? "Testing…" : "Test connection"}
          </Button>
        </div>
        {testResult && (
          <p className="t-small mt-2">
            {testResult.engine} returned {testResult.bytes.toLocaleString()} bytes
            of audio — the engine works.
          </p>
        )}
        {selected && !selected.enabled && (
          <p className="t-small mt-2" style={{ color: "var(--color-warn, #92400e)" }}>
            {selected.label} is selected but switched off below.
          </p>
        )}
      </Card>

      {form.enabled && selected?.enabled && <VoicePicker engineLabel={selected.label} />}
    </>
  )
}

function EngineCard({
  engine,
  isSelected,
  keyDraft,
  onKeyDraft,
  onChange,
}: {
  engine: AudiobookEngine
  isSelected: boolean
  keyDraft: string
  onKeyDraft: (v: string) => void
  onChange: (patch: Partial<AudiobookEngine>) => void
}) {
  return (
    <Card className="mt-6">
      <div className="mb-2 flex items-baseline justify-between" style={{ gap: 12 }}>
        <h3 className="t-h3" style={{ margin: 0 }}>
          {engine.label}
          {isSelected && (
            <span className="t-small" style={{ marginLeft: 8, fontWeight: 400 }}>
              · in use
            </span>
          )}
        </h3>
        <Toggle
          label="Enabled"
          checked={engine.enabled}
          onChange={(v) => onChange({ enabled: v })}
        />
      </div>

      <p className="t-small mb-4">{ENGINE_NOTES[engine.id]}</p>

      {engine.needsBaseUrl || engine.baseUrl ? (
        <Field label="Base URL">
          <Input
            value={engine.baseUrl}
            placeholder={engine.needsBaseUrl ? "https://…" : ""}
            onChange={(e) => onChange({ baseUrl: e.target.value })}
          />
        </Field>
      ) : null}

      <Field label="API key">
        <Input
          type="password"
          value={keyDraft}
          placeholder={
            engine.keySet
              ? "stored — leave blank to keep it"
              : engine.id === "openai"
                ? "optional for a local engine"
                : "required"
          }
          onChange={(e) => onKeyDraft(e.target.value)}
        />
      </Field>

      {engine.needsModel && (
        <Field label="Model">
          <Input value={engine.model} onChange={(e) => onChange({ model: e.target.value })} />
        </Field>
      )}

      <Field label="Default voice">
        <Input
          value={engine.defaultVoice}
          placeholder={engine.id === "azure" ? "en-US-JennyNeural" : "alloy"}
          onChange={(e) => onChange({ defaultVoice: e.target.value })}
        />
      </Field>

      <Field label="Price per million characters (USD)">
        <Input
          type="number"
          min={0}
          step="0.01"
          value={String(engine.pricePerMillionChars)}
          onChange={(e) => onChange({ pricePerMillionChars: Number(e.target.value) })}
        />
      </Field>
      <p className="t-small" style={{ marginTop: -4 }}>
        Drives the cost shown before each run. Prefilled from what the engine
        charged when this version shipped — prices move, so it is yours to
        correct. Zero is right for a local engine.
      </p>
    </Card>
  )
}

// VoicePicker exists to answer "what can I type into Default voice",
// which is otherwise unanswerable for ElevenLabs and Azure — their voice
// ids are account-specific and hundreds long respectively.
function VoicePicker({ engineLabel }: { engineLabel: string }) {
  const [open, setOpen] = useState(false)
  const voices = useQuery({
    queryKey: audiobookVoicesQueryKey,
    queryFn: fetchAudiobookVoices,
    // Only on request: this is a live call to the engine, and it is
    // useless until a key is actually saved.
    enabled: open,
  })

  return (
    <Card className="mt-6">
      <h3 className="t-h3 mb-2">Available voices</h3>
      {!open ? (
        <Button variant="outline" onClick={() => setOpen(true)}>
          Load voices from {engineLabel}
        </Button>
      ) : voices.isLoading ? (
        <p className="t-small">Asking {engineLabel}…</p>
      ) : voices.error ? (
        <p className="t-small" style={{ color: "var(--color-warn, #92400e)" }}>
          Could not list voices: {(voices.error as { message?: string }).message}
        </p>
      ) : (
        <>
          <p className="t-small mb-2">
            Copy an id into the engine's Default voice field above.
          </p>
          <ul
            style={{
              margin: 0,
              paddingLeft: 0,
              listStyle: "none",
              maxHeight: 260,
              overflowY: "auto",
            }}
          >
            {(voices.data ?? []).map((v) => (
              <li
                key={v.id}
                className="t-small"
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  gap: 12,
                  padding: "3px 0",
                }}
              >
                <span>{v.label}</span>
                <code style={{ opacity: 0.75 }}>{v.id}</code>
              </li>
            ))}
          </ul>
        </>
      )}
    </Card>
  )
}
