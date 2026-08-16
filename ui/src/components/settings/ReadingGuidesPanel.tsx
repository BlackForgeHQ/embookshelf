import type { GuideRunEstimate, ReadingGuideSettings } from "@/api/guides"
import {
  guideEstimateQuery,
  readingGuideSettingsQuery,
  saveReadingGuideSettings,
  startGuideRun,
  testReadingGuide,
} from "@/api/guides"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { pollWhile } from "@/lib/artifactRun"
import { useConnectionTest } from "@/hooks/useConnectionTest"
import { BulkRunCard } from "@/components/settings/BulkRunCard"
import { Panel, SaveRow } from "@/components/settings/Panel"
import { useSettingsDraft } from "@/hooks/useSettingsDraft"
import {
  Card,
  ConnectionTestReport,
  Field,
  Select,
  Toggle,
} from "@/components/SettingsShared"
import { SecretInput } from "@/components/settings/SecretInput"
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
  {
    label: "OpenAI",
    baseUrl: "https://api.openai.com/v1",
    authStyle: "bearer",
    hint: "needs an API key",
  },
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

export function ReadingGuidesPanel() {
  const draft = useSettingsDraft({
    query: readingGuideSettingsQuery,
    initial: emptyForm,
    save: saveReadingGuideSettings,
    successToast: "Reading guide settings saved.",
    toPayload: (form, secrets) => ({
      ...form,
      apiKey: secrets.value("apiKey"),
      keySet: secrets.stillSet("apiKey", form.keySet),
    }),
  })

  // Save first: the test reads the stored row, so an unsaved key would
  // test the previous configuration and report a confusing result.
  const test = useConnectionTest({
    test: testReadingGuide,
    read: (res) => ({
      ok: res.ok,
      message: res.ok
        ? `Endpoint replied: "${res.reply}"`
        : `Endpoint refused: ${res.error}`,
    }),
  })

  const form = draft.value
  const apiKey = draft.secret("apiKey")
  const update = <TKey extends keyof ReadingGuideSettings>(
    key: TKey,
    value: ReadingGuideSettings[TKey]
  ) => draft.patch(key, value)


  return (
    <Panel title="Reading guides" loading={draft.loading}>

      <Card>
        <p className="t-small mb-4">
          Generates a short orientation for each book: what it is about, who it
          suits, who should skip it. Books are sent to the endpoint below, so
          point it at a local model if you would rather they stayed here.
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
        <SecretInput
          label="API key"
          noun="key"
          secret={apiKey}
          stored={form.keySet}
          placeholder="not needed for a local model"
        />
        <Field label="Credential header">
          <Select
            value={form.authStyle}
            onChange={(v) =>
              update("authStyle", v as ReadingGuideSettings["authStyle"])
            }
            options={[
              {
                value: "bearer",
                label: "Authorization: Bearer (OpenAI, Ollama, OpenRouter)",
              },
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
          About {Math.round(form.textCap / 4).toLocaleString()} tokens, roughly
          the first {Math.round(form.textCap / 1600)} pages. Only EPUBs send
          text; PDFs, comics and audiobooks are described from their metadata.
        </p>

        <Toggle
          label="Request JSON mode"
          hint="Enable only if your server supports response_format. Guides work without it."
          checked={form.requestJsonMode}
          onChange={(v) => update("requestJsonMode", v)}
        />

        <SaveRow draft={draft} onSave={draft.save} align="start">
          <Button
            variant="outline"
            size="sm"
            disabled={test.running}
            onClick={() => test.run(undefined)}
            title="Sends one short prompt to the endpoint"
          >
            {test.running ? "Testing…" : "Test connection"}
          </Button>
        </SaveRow>
        <ConnectionTestReport outcome={test.outcome} />
      </Card>

      {form.enabled && <GuideRunCard />}
    </Panel>
  )
}

// GuideRunCard shows what a run would cost before starting it. ADR-0024
// §4: cost follows visibly from an explicit action, and a number nobody
// sees until the bill arrives does not qualify.
function GuideRunCard() {
  const estimate = useApiQuery(guideEstimateQuery, {
    // Poll while books still need a guide; stops on its own once
    // nothing is outstanding. Cadence and predicate from lib/artifactRun.
    ...pollWhile<GuideRunEstimate>((d) => !!d?.books),
  })

  const runMut = useApiMutation(startGuideRun, {
    successToast: (res: { queued: number }) =>
      res.queued === 0
        ? "Every book already has a guide."
        : `Queued ${res.queued} book${res.queued === 1 ? "" : "s"}.`,
  })

  const est = estimate.data
  // Something is in flight when the run mutation just fired, or when
  // the count is still falling between polls. Neither is authoritative
  // — a hint for the label, not a claim about the queue.
  const working = runMut.isPending || (estimate.isFetching && (est?.books ?? 0) > 0)

  return (
    <BulkRunCard
      title="Generate for the whole library"
      checkingText="Checking what needs a guide…"
      run={runMut}
      view={
        est && {
          total: est.totalBooks,
          done: est.booksWithGuide,
          candidates: est.books,
          working,
          coverageLabel: `${est.booksWithGuide.toLocaleString()} of ${est.totalBooks.toLocaleString()} books have a guide`,
          progressLabel: "Reading guide coverage",
          allDoneText: "Every book already has a guide.",
          runLabel: `Generate ${est.books.toLocaleString()} guide${est.books === 1 ? "" : "s"}`,
          notes:
            est.books > 0 ? (
              <>
                <p className="t-small mb-1">
                  {est.books.toLocaleString()} book{est.books === 1 ? "" : "s"}{" "}
                  still need{est.books === 1 ? "s" : ""} one,{" "}
                  {est.fullTextBooks.toLocaleString()} of them read in full.
                  {working ? " Generating…" : null}
                </p>
                <p className="t-small mb-4">
                  Up to <strong>{est.maxInputTokens.toLocaleString()}</strong>{" "}
                  input tokens. This is a ceiling: it assumes every book fills
                  the cap. Books whose guide you edited by hand are skipped.
                </p>
              </>
            ) : null,
        }
      }
    />
  )
}
