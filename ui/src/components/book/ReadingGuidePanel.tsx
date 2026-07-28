import { useState } from "react"

import type { ApiError } from "@/api/client"
import type { ReadingGuide } from "@/api/guides"
import { bookGuideQuery, generateBookGuide, saveBookGuide } from "@/api/guides"
import { meQuery } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { Button } from "@/components/ui/button"
import { Icon } from "@/components/Icon"

// The four questions a reader has before committing to a book. Order is
// deliberate: what it is, then whether it is for you, then what it does.
const SECTIONS: Array<{ key: keyof EditDraft; label: string }> = [
  { key: "about", label: "What it's about" },
  { key: "audience", label: "Who it's for" },
  { key: "notFor", label: "Who should skip it" },
  { key: "problems", label: "Problems it solves" },
]

type EditDraft = {
  about: string
  audience: string
  notFor: string
  problems: string
}

function toDraft(g: ReadingGuide): EditDraft {
  return {
    about: g.about,
    audience: g.audience,
    notFor: g.notFor,
    problems: g.problems,
  }
}

export function ReadingGuidePanel({ bookId }: { bookId: string }) {
  const me = useApiQuery(meQuery)
  const isAdmin = me.data?.role === "admin"

  const guide = useApiQuery(bookGuideQuery(bookId))

  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<EditDraft | null>(null)

  const generateMut = useApiMutation(generateBookGuide, {
    // 202: the work is queued. The guide.updated SSE event busts the
    // cache when it lands, so there is nothing to refetch here.
    successToast: "Generating — this can take a minute.",
    errorToast: (err: ApiError) =>
      err.code === "GUIDES_DISABLED"
        ? "Reading guides aren't configured. An admin can set this up in Settings."
        : err.message,
  })

  const saveMut = useApiMutation(saveBookGuide, {
    successToast: "Reading guide saved.",
    onSuccess: () => {
      setEditing(false)
      setDraft(null)
    },
  })

  if (guide.isLoading) {
    return <p className="t-small">Loading…</p>
  }

  const g = guide.data

  if (!g) {
    if (!isAdmin) {
      return (
        <p className="t-small">
          No reading guide yet. An administrator can generate one — it asks a
          language model and spends the instance's key, so it is theirs to
          start.
        </p>
      )
    }
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 640 }}>
        <p className="t-small">
          No reading guide yet. Generating one asks a language model what this
          book is about, who it suits, and who should skip it.
        </p>
        <div>
          <Button
            variant="outline"
            size="sm"
            disabled={generateMut.isPending}
            onClick={() => generateMut.mutate(bookId)}
          >
            <Icon name="sparkle" size={14} /> Generate reading guide
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16, maxWidth: 640 }}>
      {editing && draft ? (
        <form
          onSubmit={(e) => {
            e.preventDefault()
            saveMut.mutate({ id: bookId, edit: draft })
          }}
          style={{ display: "flex", flexDirection: "column", gap: 12 }}
        >
          {SECTIONS.map((s) => (
            <label key={s.key} style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              <span className="t-small" style={{ fontWeight: 500 }}>
                {s.label}
              </span>
              <textarea
                value={draft[s.key]}
                onChange={(e) => setDraft({ ...draft, [s.key]: e.target.value })}
                rows={3}
                style={{
                  width: "100%",
                  padding: 8,
                  border: "1px solid var(--color-rule-soft)",
                  borderRadius: 6,
                  font: "inherit",
                  resize: "vertical",
                }}
              />
            </label>
          ))}
          <div style={{ display: "flex", gap: 8 }}>
            <Button type="submit" size="sm" disabled={saveMut.isPending}>
              Save
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setEditing(false)
                setDraft(null)
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      ) : (
        SECTIONS.map((s) => {
            const text = g[s.key]
            if (!text) return null
            return (
              <section key={s.key}>
                <h4 className="t-small" style={{ fontWeight: 600, marginBottom: 4 }}>
                  {s.label}
                </h4>
                <p style={{ margin: 0, lineHeight: 1.55 }}>{text}</p>
              </section>
            )
          })
      )}

      <GuideProvenance guide={g} />

      {isAdmin && !editing && (
        <div style={{ display: "flex", gap: 8 }}>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setDraft(toDraft(g))
              setEditing(true)
            }}
          >
            Edit
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={generateMut.isPending}
            onClick={() => generateMut.mutate(bookId)}
          >
            Regenerate
          </Button>
        </div>
      )}
    </div>
  )
}

// GuideProvenance is the honest part of the panel. A metadata-only guide
// was written without the book — the model leaned on whatever it already
// knew about the title, which for an obscure or self-published book is
// close to nothing. Saying so is the whole reason source_kind is stored
// (ADR-0024 §2).
function GuideProvenance({ guide }: { guide: ReadingGuide }) {
  const metadataOnly = guide.sourceKind === "metadata"
  return (
    <p
      className="t-small"
      style={{
        margin: 0,
        paddingTop: 10,
        borderTop: "1px dashed var(--color-rule-soft)",
        color: metadataOnly ? "var(--color-warn, #92400e)" : undefined,
      }}
    >
      {guide.editedByUser ? (
        <>Edited by hand.</>
      ) : metadataOnly ? (
        <>
          Written from catalog metadata only — the model did not read this book,
          so treat specifics with care.
        </>
      ) : (
        <>Written from the book's own text.</>
      )}
      {guide.model && !guide.editedByUser ? <> · {guide.model}</> : null}
    </p>
  )
}
