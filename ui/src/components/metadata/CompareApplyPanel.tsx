// ui/src/components/metadata/CompareApplyPanel.tsx
import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"

import type { BookDetail, LockField } from "@/api/books"
import type { ApplyMatchBody, EnrichMatch } from "@/api/enrich"
import { bookQueryKey } from "@/api/books"
import { PROVIDER_LABELS, applyEnrichmentMatch } from "@/api/enrich"
import { useApiMutation } from "@/api/mutation"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Icon } from "@/components/Icon"

export type DiffRow = {
  field: string
  current: string
  next: string
  checked: boolean
  disabled: boolean
}

// Field → (label, lock-key, current accessor, next accessor) — single
// source of truth for diff rows. Order here is the rendered order.
type FieldSpec = {
  field: string
  label: string
  lockKey?: LockField
  current: (b: BookDetail) => string
  next: (m: EnrichMatch) => string
}

const FIELD_SPECS: ReadonlyArray<FieldSpec> = [
  {
    field: "title",
    label: "Title",
    lockKey: "title",
    current: (b) => b.title,
    next: (m) => m.title,
  },
  {
    field: "author",
    label: "Author(s)",
    lockKey: "author",
    current: (b) => b.author,
    next: (m) => m.authors.join(", "),
  },
  {
    field: "description",
    label: "Description",
    lockKey: "description",
    current: (b) => b.description ?? "",
    next: (m) => m.description ?? "",
  },
  {
    field: "publisher",
    label: "Publisher",
    lockKey: "publisher",
    current: (b) => b.publisher ?? "",
    next: (m) => m.publisher ?? "",
  },
  {
    field: "year",
    label: "Year",
    current: (b) => (b.year ? String(b.year) : ""),
    next: (m) => (m.year ? String(m.year) : ""),
  },
  {
    field: "isbn",
    label: "ISBN",
    lockKey: "isbn",
    current: (b) => b.isbn ?? "",
    next: (m) => m.isbn ?? "",
  },
  {
    field: "series",
    label: "Series",
    lockKey: "series",
    current: (b) => b.series ?? "",
    next: (m) => m.series ?? "",
  },
  {
    field: "language",
    label: "Language",
    lockKey: "language",
    current: (b) => b.language ?? "",
    next: (m) => m.language ?? "",
  },
  {
    field: "categories",
    label: "Categories",
    lockKey: "genres",
    current: (b) => b.genres.join(", "),
    next: (m) => (m.categories ?? []).join(", "),
  },
]

export function buildDiffRows(
  book: BookDetail,
  match: EnrichMatch
): Array<DiffRow> {
  const locks = book.locks ?? {}
  const rows: Array<DiffRow> = FIELD_SPECS.map((spec) => {
    const current = spec.current(book)
    const next = spec.next(match)
    const locked = !!(spec.lockKey && locks[spec.lockKey])
    const differs = current !== next && next !== ""
    return {
      field: spec.field,
      current,
      next,
      // Pre-check when (a) values differ AND (b) field is unlocked.
      // Locked rows never pre-check.
      checked: !locked && differs,
      disabled: locked,
    }
  })
  if (match.coverUrl) {
    const coverLocked = !!locks.cover
    rows.push({
      field: "cover",
      current: book.hasCover ? "(current cover)" : "",
      next: match.coverUrl,
      checked: !coverLocked,
      disabled: coverLocked,
    })
  }
  return rows
}

export function buildApplyBody(
  match: EnrichMatch,
  rows: ReadonlyArray<DiffRow>
): ApplyMatchBody {
  const checked = (field: string) =>
    rows.find((r) => r.field === field)?.checked === true
  // Always carry provider provenance. Server respects per-field locks
  // and we further opt fields in via per-field presence below.
  const body: ApplyMatchBody = {
    source: match.source,
    sourceId: match.sourceId,
    title: match.title,
    authors: match.authors,
  }
  if (checked("title")) body.title = match.title
  else delete (body as Partial<ApplyMatchBody>).title
  if (checked("author")) body.authors = match.authors
  else delete (body as Partial<ApplyMatchBody>).authors
  if (checked("description")) body.description = match.description
  if (checked("publisher")) body.publisher = match.publisher
  if (checked("year")) body.year = match.year
  if (checked("isbn")) body.isbn = match.isbn
  if (checked("series")) body.series = match.series
  if (checked("language")) body.language = match.language
  if (checked("categories")) body.categories = match.categories
  body.coverUrl = match.coverUrl
  body.applyCover = checked("cover")
  return body
}

export function CompareApplyPanel({
  book,
  match,
  onClose,
  onApplied,
}: {
  book: BookDetail
  match: EnrichMatch
  onClose: () => void
  onApplied: () => void
}) {
  const queryClient = useQueryClient()
  const [rows, setRows] = useState<Array<DiffRow>>(() =>
    buildDiffRows(book, match)
  )

  // Re-seed rows when a different match is selected. setState-in-effect
  // is intentional (prop→state sync, not a cascading render). We
  // intentionally exclude `book` from the deps — a refetch (SSE
  // invalidation, etc.) creates a new `book` reference but the user's
  // in-progress checkbox state must survive across those refetches.
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setRows(buildDiffRows(book, match))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [match])

  const applyMut = useApiMutation(applyEnrichmentMatch, {
    successToast: () => {
      const provider = PROVIDER_LABELS[match.source] ?? match.source
      return `Metadata applied from ${provider}.`
    },
    errorToast: (err) => err.message || "Apply failed.",
    onSuccess: (fresh) => {
      queryClient.setQueryData(bookQueryKey(book.id), fresh)
      onApplied()
    },
  })

  const toggle = (field: string, next: boolean) =>
    setRows((prev) =>
      prev.map((r) => (r.field === field ? { ...r, checked: next } : r))
    )

  const checkedCount = rows.filter((r) => r.checked && !r.disabled).length

  return (
    <aside
      aria-label="Compare and apply"
      className="flex h-full w-[320px] flex-col border-l border-(--color-rule-soft) bg-(--color-paper-0)"
    >
      <header className="flex items-center justify-between border-b border-(--color-rule-soft) p-4">
        <div>
          <div className="t-label">Compare & apply</div>
          <div className="t-small mt-1 italic">
            from {PROVIDER_LABELS[match.source] ?? match.source}
          </div>
        </div>
        <button
          type="button"
          aria-label="Close compare panel"
          onClick={onClose}
          className="cursor-pointer text-(--color-ink-3) hover:text-(--color-ink-1)"
        >
          <Icon name="close" size={14} />
        </button>
      </header>

      <div className="flex flex-1 flex-col gap-3 overflow-y-auto p-4">
        {rows.map((r) => (
          <div
            key={r.field}
            className="rounded-[3px] border border-(--color-rule-soft) p-3"
          >
            <label className="mb-2 flex items-center justify-between gap-3">
              <span className="t-label flex items-center gap-1.5">
                {FIELD_SPECS.find((s) => s.field === r.field)?.label ??
                  (r.field === "cover" ? "Cover" : r.field)}
                {r.disabled && <Icon name="lock" size={10} />}
              </span>
              <Checkbox
                checked={r.checked}
                disabled={r.disabled}
                onCheckedChange={(c) => toggle(r.field, c === true)}
                aria-label={`Apply ${
                  FIELD_SPECS.find((s) => s.field === r.field)?.label ??
                  (r.field === "cover" ? "Cover" : r.field)
                }`}
              />
            </label>
            {r.field === "cover" ? (
              <div className="flex gap-3">
                <div className="flex-1">
                  <div className="t-micro mb-1">Current</div>
                  {book.hasCover ? (
                    <img
                      src={`/api/v1/books/${book.id}/cover`}
                      alt=""
                      className="h-[120px] w-[80px] bg-(--color-paper-2) object-cover"
                    />
                  ) : (
                    <div className="t-small italic">—</div>
                  )}
                </div>
                <div className="flex-1">
                  <div className="t-micro mb-1">New</div>
                  <img
                    src={r.next}
                    alt=""
                    className="h-[120px] w-[80px] bg-(--color-paper-2) object-cover"
                  />
                </div>
              </div>
            ) : (
              <div className="flex flex-col gap-1.5">
                <div>
                  <div className="t-micro mb-0.5">Current</div>
                  <div className="line-clamp-3 text-[13px] text-(--color-ink-2)">
                    {r.current || <span className="italic">—</span>}
                  </div>
                </div>
                <div>
                  <div className="t-micro mb-0.5">New</div>
                  <div className="line-clamp-3 text-[13px] text-(--color-ink-1)">
                    {r.next || <span className="italic">—</span>}
                  </div>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>

      <footer className="flex items-center justify-between gap-2 border-t border-(--color-rule-soft) p-4">
        <span className="t-small">
          {checkedCount} field{checkedCount === 1 ? "" : "s"} selected
        </span>
        <div className="flex gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={onClose}
            disabled={applyMut.isPending}
          >
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={() =>
              applyMut.mutate({
                bookId: book.id,
                body: buildApplyBody(match, rows),
              })
            }
            disabled={applyMut.isPending || checkedCount === 0}
          >
            {applyMut.isPending ? "Applying…" : "Apply selected"}
          </Button>
        </div>
      </footer>
    </aside>
  )
}
