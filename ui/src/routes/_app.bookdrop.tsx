import { useEffect, useMemo, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import type { ReactNode } from "react"

import type { ApiError } from "@/api/client"
import type { BookDropItem, BookDropUploadResult } from "@/api/bookdrop"
import type { Library } from "@/api/books"
import {
  approveBookDrop,
  bookdropCoverUrl,
  bookdropFileUrl,
  bookdropQuery,
  bookdropQueryKey,
  bookdropView,
  isTerminalState,
  putBookDropCover,
  rejectBookDrop,
  uploadBookDrop,
} from "@/api/bookdrop"
import { useApiMutation } from "@/api/mutation"
import { renderPdfPageOneJpeg } from "@/lib/pdfCover"
import { librariesQuery } from "@/api/books"
import { useApiQuery } from "@/api/query"
import { Icon } from "@/components/Icon"
import { Notice } from "@/components/Notice"
import { TopBar } from "@/components/TopBar"
import { ProgressBar } from "@/components/ProgressBar"
import { formatBytes } from "@/lib/format"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

export const Route = createFileRoute("/_app/bookdrop")({
  component: BookDrop,
})

function BookDrop() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const queue = useApiQuery(bookdropQuery)
  const libraries = useApiQuery(librariesQuery)

  const active = useMemo(
    () =>
      // Still in the queue: anything not yet finished with.
      (queue.data ?? []).filter((i) => !isTerminalState(i.state)),
    [queue.data]
  )

  const [selectedId, setSelectedId] = useState<string | undefined>(undefined)
  const current = active.find((i) => i.id === selectedId) ?? active[0]
  if (current && current.id !== selectedId) {
    queueMicrotask(() => setSelectedId(current.id))
  }

  // Both report inline: the detail pane below renders `error` in the
  // place the reviewer is already looking, above the row that refused.
  const approveMut = useApiMutation(approveBookDrop, {
    reportErrors: "inline",
    onSuccess: (book) => {
      void navigate({ to: "/book/$id", params: { id: book.id } })
    },
  })

  const rejectMut = useApiMutation(rejectBookDrop, { reportErrors: "inline" })

  const error = approveMut.error ?? rejectMut.error

  // The sweep is the reviewable phase, not everything approvable: a
  // failed row can be approved one at a time, by someone who has read
  // its error, but bulk approve would import it with nobody looking.
  const sweepable = active.filter((i) => bookdropView(i).phase === "reviewable")
  const readyCount = sweepable.length

  return (
    <div className="bdrop-shell fade-in">
      <TopBar
        title="BookDrop"
        subtitle="Drop files into /bookdrop and they'll appear here for review before joining your library."
        right={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="text-(--color-ink-2) hover:text-(--color-ink-1)"
              onClick={() =>
                queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
              }
            >
              <Icon name="refresh" size={13} /> Rescan
            </Button>
            <Button
              variant="accent"
              size="sm"
              disabled={approveMut.isPending || readyCount === 0}
              onClick={() => {
                for (const item of sweepable) {
                  approveMut.mutate({ id: item.id })
                }
              }}
            >
              <Icon name="check" size={13} /> Approve{" "}
              {readyCount > 0 ? readyCount : "all"}
            </Button>
          </div>
        }
      />

      <div className="bdrop-grid">
        <aside className="bdrop-rail">
          <div className="bdrop-rail-inner">
            <div className="px-5 pt-5 pb-4">
              <DropZone
                onUploaded={() =>
                  queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
                }
              />
            </div>

            <RailSectionHeader label="In queue" count={active.length} />
            {queue.isLoading && <RailEmpty>Loading queue…</RailEmpty>}
            {queue.isError && (
              <Notice className="mx-5 my-2">
                Failed to load the ingest queue.
              </Notice>
            )}
            {active.length === 0 && !queue.isLoading && !queue.isError && (
              <RailEmpty>
                Queue is empty. Drop a file above or into{" "}
                <span className="mono">/bookdrop</span>.
              </RailEmpty>
            )}
            <div>
              {active.map((f, i) => (
                <QueueRow
                  key={f.id}
                  item={f}
                  index={i}
                  selected={selectedId === f.id}
                  onSelect={() => setSelectedId(f.id)}
                />
              ))}
            </div>
          </div>
        </aside>

        <main className="bdrop-detail">
          {current ? (
            <DetailPane
              item={current}
              libraries={libraries.data ?? []}
              error={error}
              busy={approveMut.isPending || rejectMut.isPending}
              onApprove={(libraryId) =>
                approveMut.mutate({ id: current.id, libraryId })
              }
              onReject={() => rejectMut.mutate(current.id)}
            />
          ) : (
            <EmptyDetail />
          )}
        </main>
      </div>
    </div>
  )
}

function RailSectionHeader({
  label,
  count,
  action,
}: {
  label: string
  count: number
  action?: ReactNode
}) {
  return (
    <div className="bdrop-section-header">
      <span className="bdrop-section-header-label">
        {label}
        <span className="bdrop-section-header-count">{count}</span>
      </span>
      {action}
    </div>
  )
}

function RailEmpty({ children }: { children: ReactNode }) {
  return (
    <div className="t-small px-6 py-3 text-(--color-ink-3) italic">
      {children}
    </div>
  )
}

const ACCEPT_EXTS = ".epub,.pdf,.cbz,.cbr,.cb7,.mp3,.m4a,.m4b,.mobi,.azw3,.fb2"

function DropZone({ onUploaded }: { onUploaded: () => void }) {
  const inputRef = useRef<HTMLInputElement | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [progress, setProgress] = useState(0)
  const [results, setResults] = useState<Array<BookDropUploadResult> | null>(
    null
  )
  const [err, setErr] = useState<string | null>(null)

  const upload = async (files: Array<File>) => {
    if (!files.length || uploading) return
    setUploading(true)
    setErr(null)
    setResults(null)
    setProgress(0)
    try {
      const out = await uploadBookDrop(files, (loaded, total) => {
        setProgress(total > 0 ? loaded / total : 0)
      })
      setResults(out)
      onUploaded()
    } catch (e) {
      setErr((e as { message?: string }).message ?? "upload failed")
    } finally {
      setUploading(false)
      setProgress(0)
    }
  }

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const files = Array.from(e.dataTransfer.files)
    void upload(files)
  }

  const failedCount = results?.filter((r) => r.error).length ?? 0
  const okCount = results?.filter((r) => r.item).length ?? 0

  return (
    <div>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault()
            inputRef.current?.click()
          }
        }}
        className="bdrop-drop"
        data-drag={dragOver}
        style={{ cursor: uploading ? "wait" : "pointer" }}
      >
        <span className="bdrop-drop-stamp">Bookdrop</span>
        <div className="bdrop-drop-icon" aria-hidden>
          <Icon name="upload" size={18} />
        </div>
        <div className="font-serif text-[15.5px] leading-tight font-medium">
          {uploading
            ? "Uploading…"
            : dragOver
              ? "Drop to queue"
              : "Drop files or click to browse"}
        </div>
        <div className="t-small mt-1 text-[12px] text-(--color-ink-3) italic">
          EPUB · PDF · CBZ · MOBI · AZW3 · FB2 · audio
        </div>

        {uploading && (
          <ProgressBar
            value={progress}
            label="Upload progress"
            style={{ marginTop: 12, height: 3 }}
          />
        )}

        <input
          ref={inputRef}
          type="file"
          multiple
          accept={ACCEPT_EXTS}
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            void upload(files)
            e.target.value = ""
          }}
          className="hidden"
        />
      </div>

      {err && <Notice className="mt-2.5">{err}</Notice>}

      {results && (
        <div className="mt-2.5 rounded-[3px] border border-(--color-rule-soft) bg-(--color-paper-0) px-3 py-2 text-[12.5px]">
          <div className={failedCount ? "mb-1" : ""}>
            <span className="font-medium">{okCount}</span> queued
            {failedCount ? (
              <>
                {" · "}
                <span className="font-medium text-(--color-accent-ink)">
                  {failedCount} failed
                </span>
              </>
            ) : null}
          </div>
          {results
            .filter((r) => r.error)
            .map((r) => (
              <div
                key={r.filename}
                className="mono text-[10.5px] text-(--color-accent-ink)"
              >
                {r.filename}: {r.error}
              </div>
            ))}
        </div>
      )}
    </div>
  )
}

function QueueRow({
  item,
  index,
  selected,
  onSelect,
}: {
  item: BookDropItem
  index: number
  selected: boolean
  onSelect: () => void
}) {
  const view = bookdropView(item)
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      data-selected={selected}
      // The row shows the extracted title, so two drops of the same
      // fixture are indistinguishable in the DOM. This is what lets a
      // caller address one exact row it already knows the id of.
      data-item-id={item.id}
      className="bdrop-queue-row card-row"
      style={{ ["--bdrop-i" as string]: index }}
    >
      <span className="bdrop-format-tag" aria-hidden>
        {item.format}
      </span>
      <div className="min-w-0 flex-1">
        <div className="bdrop-row-title" data-missing={!item.title}>
          {item.title || "Could not detect metadata"}
        </div>
        <div className="bdrop-row-meta">
          <span className="bdrop-dot" data-state={item.state} />
          <span>{view.label}</span>
          <span className="bdrop-row-meta-divider" aria-hidden />
          <span>{formatBytes(item.fileSize)}</span>
          {item.author && (
            <>
              <span className="bdrop-row-meta-divider" aria-hidden />
              <span className="max-w-[120px] truncate">{item.author}</span>
            </>
          )}
        </div>
        {view.showProgress && (
          <div className="bdrop-row-progress" aria-hidden>
            <span />
          </div>
        )}
      </div>
    </button>
  )
}

function DetailPane({
  item,
  libraries,
  error,
  busy,
  onApprove,
  onReject,
}: {
  item: BookDropItem
  libraries: Array<Library>
  error: ApiError | null
  busy: boolean
  onApprove: (libraryId?: string) => void
  onReject: () => void
}) {
  const view = bookdropView(item)

  return (
    <div className="bdrop-detail-inner">
      {error && <Notice className="mb-6">{error.message}</Notice>}

      <div className="bdrop-detail-head">
        <div className="min-w-0">
          <p className="bdrop-eyebrow">{view.eyebrow}</p>
          <h2 className="bdrop-title wrap-break-word">
            {item.title || "Untitled file"}
          </h2>
        </div>
        <span className="bdrop-state-ribbon">
          <span className="bdrop-dot" data-state={item.state} />
          {view.label}
        </span>
      </div>

      <div className="bdrop-filepath" title={item.path}>
        {item.path}
      </div>

      <div className="bdrop-content">
        <CoverPanel item={item} />

        <div className="min-w-0">
          <dl className="bdrop-meta-grid">
            <MetaCell
              label="Title"
              value={item.title}
              missing="Could not detect"
            />
            <MetaCell
              label="Author"
              value={item.author}
              missing="Could not detect"
            />
            <MetaCell label="Format" value={item.format} mono />
            <MetaCell label="Size" value={formatBytes(item.fileSize)} mono />
            <MetaCell label="Language" value={item.language} mono missing="—" />
            <MetaCell label="State" value={view.label} mono />
          </dl>

          {item.description && (
            <div className="bdrop-desc">
              <p className="t-label mb-2">Description</p>
              <p className="bdrop-desc-body">{item.description}</p>
            </div>
          )}

          {view.showError && (
            <div className="bdrop-error">
              <strong className="font-medium not-italic">
                Processing error.
              </strong>{" "}
              {item.errorMsg}
            </div>
          )}

          <div className="bdrop-actions">
            <ApprovalBar
              item={item}
              libraries={libraries}
              disabled={busy}
              onApprove={onApprove}
              onReject={onReject}
            />
          </div>
        </div>
      </div>
    </div>
  )
}

function EmptyDetail() {
  return (
    <div className="bdrop-empty">
      <div className="bdrop-empty-art" aria-hidden>
        <span className="bdrop-empty-art-sheet" />
        <span className="bdrop-empty-art-sheet" />
        <span className="bdrop-empty-art-sheet" />
      </div>
      <p className="t-label mb-3">Nothing to review</p>
      <p
        className="font-serif text-[15px] text-(--color-ink-2) italic"
        style={{ maxWidth: 360, lineHeight: 1.55 }}
      >
        Drop a file into the queue to start reviewing metadata before it joins a
        library.
      </p>
    </div>
  )
}

function CoverPanel({ item }: { item: BookDropItem }) {
  const queryClient = useQueryClient()
  // Set guards against duplicate uploads on re-render / StrictMode
  // double-effect. Survives across renders for the lifetime of the
  // component instance. Relies on backend 409 idempotency for
  // cross-mount duplicate suppression.
  const uploadedRef = useRef<Set<string>>(new Set())
  // Whether a pre-approval cover can still be uploaded for this row —
  // the client half of the state gate in BookDropPutCover, which the
  // view owns and documents.
  const { canUploadCover } = bookdropView(item)

  useEffect(() => {
    if (item.format !== "PDF") return
    if (item.hasCover) return
    if (!canUploadCover) return
    if (uploadedRef.current.has(item.id)) return
    uploadedRef.current.add(item.id)
    const ctrl = new AbortController()
    void (async () => {
      const url = bookdropFileUrl(item.id)
      const blob = await renderPdfPageOneJpeg(url, { signal: ctrl.signal })
      if (ctrl.signal.aborted || !blob) return
      try {
        await putBookDropCover(item.id, blob)
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      } catch (err) {
        console.warn("auto cover upload failed", err)
      }
    })()
    return () => {
      ctrl.abort()
    }
  }, [item.id, item.format, item.hasCover, canUploadCover, queryClient])

  return (
    <div className="bdrop-cover-frame">
      {item.hasCover ? (
        <img src={bookdropCoverUrl(item.id)} alt="" />
      ) : (
        <div className="bdrop-cover-blank">
          <Icon name="book" size={28} className="mb-2 text-(--color-ink-3)" />
          <span className="bdrop-cover-blank-label">
            {item.format === "PDF" && canUploadCover
              ? "rendering cover…"
              : "no cover detected"}
          </span>
        </div>
      )}
    </div>
  )
}

function ApprovalBar({
  item,
  libraries,
  disabled,
  onApprove,
  onReject,
}: {
  item: BookDropItem
  libraries: Array<Library>
  disabled: boolean
  onApprove: (libraryId?: string) => void
  onReject: () => void
}) {
  const [libraryId, setLibraryId] = useState<string | undefined>(
    libraries[0]?.id
  )
  const { canApprove } = bookdropView(item)

  return (
    <>
      {libraries.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="t-label">Add to</span>
          <Select
            value={libraryId}
            onValueChange={(v) => setLibraryId(v || undefined)}
          >
            <SelectTrigger size="sm" className="w-auto min-w-[160px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {libraries.map((lib) => (
                <SelectItem key={lib.id} value={lib.id}>
                  {lib.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}
      <Button
        disabled={disabled || !canApprove}
        onClick={() => onApprove(libraryId)}
      >
        <Icon name="check" size={13} /> Approve import
      </Button>
      <span className="spacer" />
      <Button
        variant="ghost"
        disabled={disabled}
        className="text-(--color-accent-ink) hover:bg-(--color-accent-soft) hover:text-(--color-accent-ink)"
        onClick={onReject}
      >
        Discard file
      </Button>
    </>
  )
}

function MetaCell({
  label,
  value,
  missing,
  mono,
}: {
  label: string
  value?: string | null
  missing?: string
  mono?: boolean
}) {
  const v = (value ?? "").trim()
  const isMissing = v === ""
  return (
    <div className="bdrop-meta-cell">
      <dt className="bdrop-meta-label">{label}</dt>
      <dd
        className={`bdrop-meta-value${mono ? " mono" : ""}`}
        data-missing={isMissing}
      >
        {isMissing ? (missing ?? "—") : v}
      </dd>
    </div>
  )
}
