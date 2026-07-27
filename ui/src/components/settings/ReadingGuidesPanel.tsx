import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"

import type { GuideTestResult, ReadingGuideSettings } from "@/api/guides"
import {
  fetchGuideEstimate,
  fetchReadingGuideSettings,
  guideEstimateQueryKey,
  readingGuideSettingsQueryKey,
  saveReadingGuideSettings,
  startGuideRun,
  testReadingGuide,
} from "@/api/guides"
import { useApiMutation } from "@/api/mutation"
import { AdminGate, Card, Field, Select, Toggle } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

// Roughly the opening 30-40 pages. A 300-page EPUB extracts to about nine
// times this, so the cap binds for most books — deliberately, since it is
// what the run costs.
const DEFAULT_TEXT_CAP = 48_000

const emptyForm: ReadingGuideSettings = {
  enabled: false,
  baseUrl: "",
  model: "",
  keySet: false,
  authStyle: "bearer",
  language: "en",
  textCap: DEFAULT_TEXT_CAP,
  requestJsonMode: false,
}

// Presets for the endpoints people actually use. Ollama first: it is the
// reason the adapter is OpenAI-compatible rather than vendor-specific —
// pointing at localhost keeps every book on the operator's own machine.
const PRESETS: ReadonlyArray<{
  label: string
  baseUrl: string
  authStyle: ReadingGuideSettings["authStyle"]
  hint: string
}> = [
  {
    label: "Ollama (local)",
    baseUrl: "http://localhost:11434/v1",
    authStyle: "bearer",
    hint: "nothing leaves this machine",
  },
  { label: "OpenAI", baseUrl: "https://api.openai.com/v1", authStyle: "bearer", hint: "needs an API key" },
  {
    label: "OpenRouter",
    baseUrl: "https://openrouter.ai/api/v1",
    authStyle: "bearer",
    hint: "needs an API key",
  },
  {
    // Azure exposes the OpenAI-compatible surface at /openai/v1 — note
    // the base stops there, the client appends /chat/completions. A
    // resource URL ending in /responses is the other API and will 404.
    label: "Azure AI",
    baseUrl: "https://<resource>.services.ai.azure.com/openai/v1",
    authStyle: "api-key",
    hint: "uses an api-key header, not a bearer token",
  },
]

export function ReadingGuidesPanel({ isAdmin }: { isAdmin: boolean }) {
  const settings = useQuery({
    queryKey: readingGuideSettingsQueryKey,
    queryFn: fetchReadingGuideSettings,
    enabled: isAdmin,
  })

  const [form, setForm] = useState<ReadingGuideSettings>(emptyForm)
  // The key lives outside `form` for the same reason the SMTP password
  // does: the server never sends it back, so an empty submit must mean
  // "leave the stored one alone" rather than "clear it".
  const [keyDraft, setKeyDraft] = useState("")

  useEffect(() => {
    if (settings.data) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setForm(settings.data)
      setKeyDraft("")
    }
  }, [settings.data])

  const [testResult, setTestResult] = useState<GuideTestResult | null>(null)
  // Save first: the test reads the stored row, so an unsaved key would
  // test the previous configuration and report a confusing result.
  const testMut = useApiMutation(testReadingGuide, {
    onSuccess: (res: GuideTestResult) => setTestResult(res),
  })

  const saveMut = useApiMutation(saveReadingGuideSettings, {
    successToast: "Reading guide settings saved.",
    onSuccess: () => setKeyDraft(""),
  })

  if (!isAdmin) return <AdminGate label="Reading guides" />

  const update = <TKey extends keyof ReadingGuideSettings>(
    key: TKey,
    value: ReadingGuideSettings[TKey]
  ) => setForm((f) => ({ ...f, [key]: value }))

  return (
    <>
      <h2 className="t-h2 mb-6">Reading guides</h2>

      <Card>
        <p className="t-small mb-4">
          Generates a short orientation for each book — what it is about, who
          it suits, who should skip it. Books are sent to the endpoint below,
          so point it at a local model if you would rather they stayed here.
        </p>

        <Toggle
          label="Enable reading guides"
          hint="Nothing generates on its own. Guides are created per book or by a run you start."
          checked={form.enabled}
          onChange={(v) => update("enabled", v)}
        />

        <div className="mt-4 flex flex-wrap gap-2">
          {PRESETS.map((p) => (
            <Button
              key={p.label}
              variant="outline"
              size="sm"
              onClick={() => {
                update("baseUrl", p.baseUrl)
                update("authStyle", p.authStyle)
              }}
              title={p.hint}
            >
              {p.label}
            </Button>
          ))}
        </div>

        <Field label="Base URL">
          <Input
            value={form.baseUrl}
            onChange={(e) => update("baseUrl", e.target.value)}
            placeholder="http://localhost:11434/v1"
          />
        </Field>
        <Field label="Model">
          <Input
            value={form.model}
            onChange={(e) => update("model", e.target.value)}
            placeholder="llama3.1 / gpt-4o-mini"
          />
        </Field>
        <Field label={form.keySet ? "API key (stored — leave blank to keep)" : "API key"}>
          <Input
            type="password"
            value={keyDraft}
            onChange={(e) => setKeyDraft(e.target.value)}
            placeholder={form.keySet ? "••••••••" : "not needed for a local model"}
          />
        </Field>
        <Field label="Credential header">
          <Select
            value={form.authStyle}
            onChange={(v) => update("authStyle", v as ReadingGuideSettings["authStyle"])}
            options={[
              { value: "bearer", label: "Authorization: Bearer (OpenAI, Ollama, OpenRouter)" },
              { value: "api-key", label: "api-key (Azure)" },
            ]}
          />
        </Field>
        <Field label="Guide language">
          <Input
            value={form.language}
            onChange={(e) => update("language", e.target.value)}
            placeholder="en"
          />
        </Field>
        <Field label="Book text sent per guide (characters)">
          <Input
            type="number"
            value={form.textCap}
            onChange={(e) => update("textCap", Number(e.target.value))}
          />
        </Field>
        <p className="t-small mb-4">
          About {Math.round(form.textCap / 4).toLocaleString()} tokens — roughly
          the first {Math.round(form.textCap / 1600)} pages. Only EPUBs send
          text; PDFs, comics and audiobooks are described from their metadata.
        </p>

        <Toggle
          label="Request JSON mode"
          hint="Enable only if your server supports response_format. Guides work without it."
          checked={form.requestJsonMode}
          onChange={(v) => update("requestJsonMode", v)}
        />

        <div className="mt-4 flex items-center gap-2">
          <Button
            disabled={saveMut.isPending}
            onClick={() =>
              saveMut.mutate(keyDraft.trim() ? { ...form, apiKey: keyDraft } : form)
            }
          >
            Save
          </Button>
          <Button
            variant="outline"
            disabled={testMut.isPending}
            onClick={() => testMut.mutate(undefined)}
            title="Sends one short prompt to the endpoint"
          >
            {testMut.isPending ? "Testing…" : "Test connection"}
          </Button>
        </div>
        {testResult && (
          <p
            className="t-small mt-2"
            style={{ color: testResult.ok ? undefined : "var(--color-warn, #92400e)" }}
          >
            {testResult.ok
              ? `Endpoint replied: "${testResult.reply}"`
              : `Endpoint refused: ${testResult.error}`}
          </p>
        )}
      </Card>

      {form.enabled && <GuideRunCard />}
    </>
  )
}

// GuideRunCard shows what a run would cost before starting it. ADR-0024
// §4: cost follows visibly from an explicit action, and a number nobody
// sees until the bill arrives does not qualify.
function GuideRunCard() {
  const estimate = useQuery({
    queryKey: guideEstimateQueryKey,
    queryFn: fetchGuideEstimate,
    // Poll while books still need a guide. Coverage is two counts on a
    // query that already runs, so this is cheap; it stops on its own once
    // nothing is outstanding rather than polling an idle instance forever.
    refetchInterval: (q) => {
      const d = q.state.data
      return d && d.books > 0 ? 4000 : false
    },
  })

  const runMut = useApiMutation(startGuideRun, {
    successToast: (res: { queued: number }) =>
      res.queued === 0
        ? "Every book already has a guide."
        : `Queued ${res.queued} book${res.queued === 1 ? "" : "s"}.`,
  })

  const est = estimate.data

  if (!est) {
    return (
      <Card className="mt-6">
        <h3 className="t-h3 mb-2">Generate for the whole library</h3>
        <p className="t-small">Checking what needs a guide…</p>
      </Card>
    )
  }

  const pct =
    est.totalBooks > 0
      ? Math.round((est.booksWithGuide / est.totalBooks) * 100)
      : 0
  // Something is in flight when the run mutation just fired, or when the
  // count is still falling between polls. Neither is authoritative — this
  // is a hint for the label, not a claim about the queue.
  const working = runMut.isPending || (estimate.isFetching && est.books > 0)

  return (
    <Card className="mt-6">
      <h3 className="t-h3 mb-2">Generate for the whole library</h3>

      {est.totalBooks > 0 && (
        <div className="mb-4">
          <div
            className="mb-1 flex items-baseline justify-between"
            style={{ gap: 12 }}
          >
            <span className="t-small">
              {est.booksWithGuide.toLocaleString()} of{" "}
              {est.totalBooks.toLocaleString()} books have a guide
            </span>
            <span className="t-small tabular-nums">{pct}%</span>
          </div>
          <div
            aria-label="Reading guide coverage"
            style={{
              height: 6,
              borderRadius: 3,
              background: "var(--color-rule-soft)",
              overflow: "hidden",
            }}
          >
            <div
              style={{
                width: `${pct}%`,
                height: "100%",
                background: "var(--color-accent, #0f766e)",
                transition: "width .4s ease",
              }}
            />
          </div>
        </div>
      )}

      {est.books === 0 ? (
        <p className="t-small">Every book already has a guide.</p>
      ) : (
        <>
          <p className="t-small mb-1">
            {est.books.toLocaleString()} book{est.books === 1 ? "" : "s"} still
            need{est.books === 1 ? "s" : ""} one,{" "}
            {est.fullTextBooks.toLocaleString()} of them read in full.
            {working ? " Generating…" : null}
          </p>
          <p className="t-small mb-4">
            Up to <strong>{est.maxInputTokens.toLocaleString()}</strong> input
            tokens. This is a ceiling — it assumes every book fills the cap.
            Books whose guide you edited by hand are skipped.
          </p>
          <Button
            variant="outline"
            disabled={runMut.isPending}
            onClick={() => runMut.mutate(undefined)}
          >
            Generate {est.books.toLocaleString()} guide
            {est.books === 1 ? "" : "s"}
          </Button>
        </>
      )}
    </Card>
  )
}
