import { useEffect, useMemo, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import type { ReactNode } from "react"

import type { ApiError } from "@/api/client"
import type {
  BookDropItem,
  BookDropState,
  BookDropUploadResult,
} from "@/api/bookdrop"
import type { Library } from "@/api/books"
import {
  approveBookDrop,
  bookdropCoverUrl,
  bookdropFileUrl,
  bookdropQueryKey,
  fetchBookDrop,
  putBookDropCover,
  rejectBookDrop,
  uploadBookDrop,
} from "@/api/bookdrop"
import { renderPdfPageOneJpeg } from "@/lib/pdfCover"
import { booksQueryKey, fetchLibraries, librariesQueryKey } from "@/api/books"
import { Icon } from "@/components/Icon"
import { TopBar } from "@/components/TopBar"
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

const STATE_LABEL: Record<BookDropState, string> = {
  ready: "ready",
  processing: "processing",
  discovered: "queued",
  failed: "failed",
  imported: "imported",
  rejected: "discarded",
}

function BookDrop() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const queue = useQuery({
    queryKey: bookdropQueryKey,
    queryFn: fetchBookDrop,
  })
  const libraries = useQuery({
    queryKey: librariesQueryKey,
    queryFn: fetchLibraries,
  })

  const { active, finished } = useMemo(() => {
    const all = queue.data ?? []
    return {
      active: all.filter(
        (i) => i.state !== "imported" && i.state !== "rejected"
      ),
      finished: all.filter(
        (i) => i.state === "imported" || i.state === "rejected"
      ),
    }
  }, [queue.data])

  const [selectedId, setSelectedId] = useState<string | undefined>(undefined)
  const current =
    queue.data?.find((i) => i.id === selectedId) ?? active[0] ?? finished[0]
  if (current && current.id !== selectedId) {
    queueMicrotask(() => setSelectedId(current.id))
  }

  const approveMut = useMutation({
    mutationFn: ({ id, libraryId }: { id: string; libraryId?: string }) =>
      approveBookDrop(id, libraryId),
    onSuccess: (book) => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      queryClient.invalidateQueries({ queryKey: booksQueryKey() })
      queryClient.invalidateQueries({ queryKey: librariesQueryKey })
      void navigate({ to: "/book/$id", params: { id: book.id } })
    },
  })

  const rejectMut = useMutation({
    mutationFn: (id: string) => rejectBookDrop(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
    },
  })

  const error = (approveMut.error ?? rejectMut.error) as unknown as
    | ApiError
    | null

  const readyCount = active.filter((i) => i.state === "ready").length

  return (
    <div className="fade-in bdrop-shell">
      <TopBar
        title="BookDrop"
        subtitle="Drop files into /bookdrop and they'll appear here for review before joining your library."
        right={
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
              }
            >
              <Icon name="refresh" size={13} /> Rescan
            </Button>
            <Button
              size="sm"
              disabled={approveMut.isPending || readyCount === 0}
              onClick={() => {
                for (const item of active) {
                  if (item.state === "ready") {
                    approveMut.mutate({ id: item.id })
                  }
                }
              }}
            >
              <Icon name="check" size={13} /> Approve {readyCount > 0 ? readyCount : "all"}
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
              <div className="mx-5 my-2 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink) text-[12.5px]">
                Failed to load the ingest queue.
              </div>
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

            {finished.length > 0 && (
              <div className="bdrop-finished">
                <RailSectionHeader
                  label="Recently processed"
                  count={finished.length}
                />
                <div>
                  {finished.map((f, i) => (
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
            )}
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
              onOpenBook={(id) =>
                void navigate({ to: "/book/$id", params: { id } })
              }
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
    <div className="t-small italic px-6 py-3 text-(--color-ink-3)">
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
        <div className="font-serif text-[15.5px] font-medium leading-tight">
          {uploading
            ? "Uploading…"
            : dragOver
              ? "Drop to queue"
              : "Drop files or click to browse"}
        </div>
        <div className="t-small italic mt-1 text-[12px] text-(--color-ink-3)">
          EPUB · PDF · CBZ · MOBI · AZW3 · FB2 · audio
        </div>

        {uploading && (
          <div className="mt-3 h-[3px] overflow-hidden rounded-[2px] bg-(--color-paper-3)">
            <div
              className="h-full bg-(--color-editorial-accent)"
              style={{
                width: `${Math.round(progress * 100)}%`,
                transition: "width 80ms linear",
              }}
            />
          </div>
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

      {err && (
        <div
          className="mt-2.5 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-[12.5px] text-(--color-accent-ink)"
          role="alert"
        >
          {err}
        </div>
      )}

      {results && (
        <div className="mt-2.5 rounded-[3px] border border-(--color-rule-soft) bg-(--color-paper-0) px-3 py-2 text-[12.5px]">
          <div className={failedCount ? "mb-1" : ""}>
            <span className="font-medium">{okCount}</span> queued
            {failedCount ? (
              <>
                {" · "}
                <span className="text-(--color-accent-ink) font-medium">
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
  const isProcessing = item.state === "processing"
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      data-selected={selected}
      className="bdrop-queue-row card-row"
      style={{ ["--bdrop-i" as string]: index }}
    >
      <span className="bdrop-format-tag" aria-hidden>
        {item.format}
      </span>
      <div className="min-w-0 flex-1">
        <div
          className="bdrop-row-title"
          data-missing={!item.title}
        >
          {item.title || "Could not detect metadata"}
        </div>
        <div className="bdrop-row-meta">
          <span className="bdrop-dot" data-state={item.state} />
          <span>{STATE_LABEL[item.state]}</span>
          <span className="bdrop-row-meta-divider" aria-hidden />
          <span>{formatBytes(item.fileSize)}</span>
          {item.author && (
            <>
              <span className="bdrop-row-meta-divider" aria-hidden />
              <span className="truncate max-w-[120px]">{item.author}</span>
            </>
          )}
        </div>
        {isProcessing && (
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
  onOpenBook,
}: {
  item: BookDropItem
  libraries: Array<Library>
  error: ApiError | null
  busy: boolean
  onApprove: (libraryId?: string) => void
  onReject: () => void
  onOpenBook: (id: string) => void
}) {
  const eyebrow =
    item.state === "imported"
      ? "Imported"
      : item.state === "rejected"
        ? "Discarded"
        : item.state === "failed"
          ? "Needs attention"
          : item.state === "ready"
            ? "Review import"
            : item.state === "processing"
              ? "Extracting metadata"
              : "Discovered"

  return (
    <div className="bdrop-detail-inner">
      {error && (
        <div
          role="alert"
          className="mb-6 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-[13px] text-(--color-accent-ink)"
        >
          {error.message}
        </div>
      )}

      <div className="bdrop-detail-head">
        <div className="min-w-0">
          <p className="bdrop-eyebrow">{eyebrow}</p>
          <h2 className="bdrop-title wrap-break-word">
            {item.title || "Untitled file"}
          </h2>
        </div>
        <span className="bdrop-state-ribbon">
          <span className="bdrop-dot" data-state={item.state} />
          {STATE_LABEL[item.state]}
        </span>
      </div>

      <div className="bdrop-filepath" title={item.path}>
        {item.path}
      </div>

      <div className="bdrop-content">
        <CoverPanel item={item} />

        <div className="min-w-0">
          <dl className="bdrop-meta-grid">
            <MetaCell label="Title" value={item.title} missing="Could not detect" />
            <MetaCell label="Author" value={item.author} missing="Could not detect" />
            <MetaCell label="Format" value={item.format} mono />
            <MetaCell label="Size" value={formatBytes(item.fileSize)} mono />
            <MetaCell label="Language" value={item.language} mono missing="—" />
            <MetaCell label="State" value={STATE_LABEL[item.state]} mono />
          </dl>

          {item.description && (
            <div className="bdrop-desc">
              <p className="t-label mb-2">Description</p>
              <p className="bdrop-desc-body">{item.description}</p>
            </div>
          )}

          {item.state === "failed" && item.errorMsg && (
            <div className="bdrop-error">
              <strong className="not-italic font-medium">Processing error.</strong>{" "}
              {item.errorMsg}
            </div>
          )}

          <div className="bdrop-actions">
            {item.state === "imported" && item.bookId ? (
              <Button onClick={() => onOpenBook(item.bookId!)}>
                <Icon name="book-open" size={13} /> Open imported book
              </Button>
            ) : item.state === "rejected" ? (
              <p className="t-small italic text-(--color-ink-3)">
                This item was discarded.
              </p>
            ) : (
              <ApprovalBar
                item={item}
                libraries={libraries}
                disabled={busy}
                onApprove={onApprove}
                onReject={onReject}
              />
            )}
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
        className="font-serif italic text-[15px] text-(--color-ink-2)"
        style={{ maxWidth: 360, lineHeight: 1.55 }}
      >
        Drop a file into the queue to start reviewing metadata before it joins
        a library.
      </p>
    </div>
  )
}

function CoverPanel({ item }: { item: BookDropItem }) {
  const queryClient = useQueryClient()
  // Set guards against duplicate uploads on re-render / StrictMode
  // double-effect. Survives across renders for the lifetime of the
  // component instance.
  const uploadedRef = useRef<Set<string>>(new Set())
  const isPreapprovalState =
    item.state === "discovered" ||
    item.state === "processing" ||
    item.state === "ready"

  useEffect(() => {
    if (item.format !== "PDF") return
    if (item.hasCover) return
    if (!isPreapprovalState) return
    if (uploadedRef.current.has(item.id)) return
    uploadedRef.current.add(item.id)
    // Explicit `: boolean` widens the type so the lint rule doesn't
    // narrow to literal-false — the cleanup closure mutates it.
    let cancelled = false as boolean
    void (async () => {
      const blob = await renderPdfPageOneJpeg(bookdropFileUrl(item.id))
      if (cancelled || !blob) return
      try {
        await putBookDropCover(item.id, blob)
        queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      } catch (err) {
        console.warn("auto cover upload failed", err)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [item.id, item.format, item.hasCover, isPreapprovalState, queryClient])

  return (
    <div className="bdrop-cover-frame">
      {item.hasCover ? (
        <img src={bookdropCoverUrl(item.id)} alt="" />
      ) : (
        <div className="bdrop-cover-blank">
          <Icon
            name="book"
            size={28}
            className="mb-2 text-(--color-ink-3)"
          />
          <span className="bdrop-cover-blank-label">
            {item.format === "PDF" && isPreapprovalState
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
  const approvable = item.state === "ready" || item.state === "failed"

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
        disabled={disabled || !approvable}
        onClick={() => onApprove(libraryId)}
      >
        <Icon name="check" size={13} /> Approve import
      </Button>
      <span className="spacer" />
      <Button
        variant="ghost"
        disabled={disabled}
        className="text-(--color-accent-ink) hover:text-(--color-accent-ink) hover:bg-(--color-accent-soft)"
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
        className={"bdrop-meta-value" + (mono ? " mono" : "")}
        data-missing={isMissing}
      >
        {isMissing ? (missing ?? "—") : v}
      </dd>
    </div>
  )
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "—"
  const units = ["B", "KB", "MB", "GB"]
  let value = n
  let u = 0
  while (value >= 1024 && u < units.length - 1) {
    value /= 1024
    u++
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[u]}`
}
