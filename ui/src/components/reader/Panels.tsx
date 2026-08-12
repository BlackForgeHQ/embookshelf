import type { ReactNode } from "react"

import type { Annotation } from "@/api/annotations"
import type { Locator } from "@/lib/locator"
import { annotationKind } from "@/api/annotations"
import { decodeLocator, locatorLabel } from "@/lib/locator"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"

/**
 * The two side panels the paged shells share (ADR-0029 §3, slice 3).
 *
 * Splitting `ReaderShell` into a text shell and a PDF shell left these
 * two behind: the notes aside was the one panel both formats open, and
 * the type panel is character-identical in both. Copying them into each
 * shell to be rid of two format ternaries would have traded a branch for
 * a hundred duplicated lines.
 *
 * What made the aside branch is passed in instead — the empty-state
 * sentence, the action above the list, and the jump-to-position button,
 * which only a shell can render because only a shell holds the
 * imperative handle, and the two handles take different arguments
 * (`goTo(href: string)` against `goTo(page: number)`).
 *
 * No state, no ref, and deliberately no query: the annotation list
 * arrives as data so each shell keeps its own query next to its own
 * memo, which is the arrangement ADR-0029 §3 asks for.
 */

type NotesPanelProps = {
  /** The book's annotations, already fetched by the shell. */
  annotations: Array<Annotation>
  loading: boolean
  /** What to say when there are none. The two shells offer different next moves. */
  emptyText: string
  /**
   * An action above the list — the PDF shell's "new note on this page".
   * The text shell has none: it annotates from the selection toolbar.
   */
  children?: ReactNode
  /**
   * The jump-to-position button for an annotation that carries a
   * locator. Returning null is the normal answer for a locator this
   * shell cannot navigate to — a page token sitting in an EPUB's notes.
   */
  renderGoTo: (locator: Locator) => ReactNode
  onDelete: (annotation: Annotation) => void
  /** True while a delete is in flight. */
  deleting: boolean
}

/** The right-hand notes aside: every annotation on this book. */
export function NotesPanel({
  annotations,
  loading,
  emptyText,
  children,
  renderGoTo,
  onDelete,
  deleting,
}: NotesPanelProps) {
  return (
    <aside
      className="panel-in-right"
      style={{
        width: 320,
        borderLeft: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-1)",
        overflow: "auto",
        padding: "18px 16px",
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        gap: 10,
      }}
    >
      <div className="t-label">Notes on this book</div>

      {children}

      {loading && (
        <div className="t-small" style={{ fontStyle: "italic" }}>
          Loading…
        </div>
      )}
      {!loading && annotations.length === 0 && (
        <div className="t-small" style={{ fontStyle: "italic" }}>
          {emptyText}
        </div>
      )}

      {annotations.map((a) => {
        const kind = annotationKind(a)
        const locator = decodeLocator(a.locator)
        return (
          <div
            key={a.id}
            style={{
              borderLeft: "3px solid var(--color-accent-soft)",
              padding: "6px 10px",
              background: "var(--color-paper-0)",
            }}
          >
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                marginBottom: 4,
              }}
            >
              <span className="t-micro" style={{ fontSize: 9.5 }}>
                {kind === "highlight"
                  ? "Highlight"
                  : kind === "highlight+note"
                    ? "Highlight · Note"
                    : "Note"}
                {/* A CFI reduces to "EPUB", which says nothing the
                    reader you are already inside doesn't — so only
                    a page carries a label worth showing here. */}
                {locator?.kind === "page" && ` · ${locatorLabel(a.locator)}`}
              </span>
              <div style={{ display: "flex", alignItems: "center", gap: 6 }}>
                {locator && renderGoTo(locator)}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => onDelete(a)}
                  disabled={deleting}
                  aria-label="Delete"
                  title="Delete"
                >
                  <Icon name="close" size={10} />
                </Button>
              </div>
            </div>
            {a.selectedText && (
              <p
                style={{
                  fontSize: 12.5,
                  lineHeight: 1.5,
                  fontStyle: "italic",
                  background: "var(--color-highlight)",
                  padding: "4px 6px",
                  marginBottom: a.note ? 6 : 0,
                }}
              >
                {a.selectedText}
              </p>
            )}
            {a.note && (
              <p style={{ fontSize: 13, lineHeight: 1.5 }}>{a.note}</p>
            )}
          </div>
        )
      })}
    </aside>
  )
}

/**
 * The floating type panel. A placeholder in both shells until per-user
 * reader preferences sync, and identical in both — which is why it is
 * here rather than written twice.
 */
export function TypePanel() {
  return (
    <div
      className="panel-in-right"
      style={{
        position: "absolute",
        top: 0,
        right: 16,
        width: 260,
        background: "var(--color-paper-0)",
        border: "1px solid var(--color-ink-3)",
        boxShadow: "0 12px 32px -8px oklch(0.2 0.02 60 / 0.22)",
        padding: "14px 16px",
        borderRadius: 2,
        zIndex: 5,
      }}
    >
      <div className="t-label" style={{ marginBottom: 10 }}>
        Reader type
      </div>
      <div
        style={{
          fontSize: 12,
          color: "var(--color-ink-3)",
          fontStyle: "italic",
        }}
      >
        Font + size controls land once per-user reader preferences sync from the
        backend.
      </div>
    </div>
  )
}
