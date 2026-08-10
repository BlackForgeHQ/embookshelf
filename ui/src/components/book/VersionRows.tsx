import { bookMarkdownQuery } from "@/api/markdown"
import { useApiQuery } from "@/api/query"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"
import { formatBytes } from "@/lib/format"

// VersionRows is the Versions tab's file list: every way this book
// exists as bytes, each row downloadable. Two sources feed it — the
// primary file (a files row, served by /books/:id/file) and the
// Markdown rendition (deliberately NOT a files row, ADR-0033 §4, so it
// comes from its own status endpoint and its own download route).
export function VersionRows({
  bookId,
  title,
  format,
}: {
  bookId: string
  title: string
  format: string
}) {
  // The rendition row appears only when ready: Versions lists artifacts
  // that exist, not work in flight — pending/failed live on the guide
  // tab and the settings card.
  const markdown = useApiQuery(bookMarkdownQuery(bookId))
  const md = markdown.data?.state === "ready" ? markdown.data : null

  return (
    <>
      <VersionRow
        icon="book"
        name={`${title}.${format.toLowerCase()}`}
        meta={`Primary · ${format}`}
        href={`/api/v1/books/${bookId}/file?download=1`}
      />
      {md && (
        <VersionRow
          icon="sparkle"
          name={`${title}.md`}
          meta={[
            "Markdown",
            md.sizeBytes ? formatBytes(md.sizeBytes) : null,
            md.converterVersion ? `converter v${md.converterVersion}` : null,
          ]
            .filter(Boolean)
            .join(" · ")}
          href={`/api/v1/books/${bookId}/markdown/file`}
          // Stale is labelled, never hidden and never auto-invalidated —
          // the audiobook rule (ADR-0025 §2) applied to the rendition.
          note={
            md.stale
              ? "Stale — converted from an older copy of this book."
              : undefined
          }
        />
      )}
    </>
  )
}

function VersionRow({
  icon,
  name,
  meta,
  href,
  note,
}: {
  icon: "book" | "sparkle"
  name: string
  meta: string
  href: string
  note?: string
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 16,
        padding: "10px 12px",
        border: "1px solid var(--color-rule-soft)",
        background: "var(--color-paper-0)",
      }}
    >
      <Icon name={icon} size={16} />
      <div className="grow">
        <div className="t-item-title">{name}</div>
        <div
          className="mono"
          style={{ fontSize: 11, color: "var(--color-ink-3)" }}
        >
          {meta}
        </div>
        {note && (
          <div
            className="t-small"
            style={{ marginTop: 2, color: "var(--color-accent-ink)" }}
          >
            {note}
          </div>
        )}
      </div>
      <Button variant="outline" size="sm" asChild>
        <a href={href} download>
          <Icon name="download" size={13} /> Download
        </a>
      </Button>
    </div>
  )
}
