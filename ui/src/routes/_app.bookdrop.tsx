import { useMemo, useRef, useState } from "react"
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
  bookdropQueryKey,
  clearProcessedBookDrop,
  fetchBookDrop,
  rejectBookDrop,
  uploadBookDrop,
} from "@/api/bookdrop"
import { booksQueryKey, fetchLibraries, librariesQueryKey } from "@/api/books"
import { Icon } from "@/components/Icon"
import { TopBar } from "@/components/TopBar"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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

// State → status dot color. Calibrated to be visible against the warm
// paper background (no more paper-3 ghost dots for "failed").
const STATUS_COLOR: Record<BookDropState, string> = {
  ready: "oklch(0.58 0.12 140)", // green
  processing: "oklch(0.65 0.10 70)", // amber
  discovered: "var(--color-ink-3)",
  failed: "var(--color-accent-ink)", // burgundy
  imported: "var(--color-ink-4)", // muted (terminal, success)
  rejected: "var(--color-ink-4)", // muted (terminal, dismissed)
}

function BookDrop() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const queue = useQuery({
    queryKey: bookdropQueryKey,
    queryFn: fetchBookDrop,
    // Realtime invalidations via SSE drive refreshes — see
    // frontend/src/api/realtime.ts. No poll interval needed; the
    // "Rescan" button in the top bar is the fallback manual trigger.
  })
  const libraries = useQuery({
    queryKey: librariesQueryKey,
    queryFn: fetchLibraries,
  })

  // Surface only the still-actionable items at the top — approved/rejected
  // rows land below under a collapsed section.
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
  const [clearConfirmOpen, setClearConfirmOpen] = useState(false)
  // Auto-select the first actionable row on first load + whenever the
  // current selection disappears (approved / rejected / deleted).
  const current =
    queue.data?.find((i) => i.id === selectedId) ?? active[0] ?? finished[0]
  if (current && current.id !== selectedId) {
    // setState-in-render is a defensible TanStack Query pattern for sync
    // effects; React will flush on the same commit.
    queueMicrotask(() => setSelectedId(current.id))
  }

  const approveMut = useMutation({
    mutationFn: ({ id, libraryId }: { id: string; libraryId?: string }) =>
      approveBookDrop(id, libraryId),
    onSuccess: (book) => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      queryClient.invalidateQueries({ queryKey: booksQueryKey() })
      queryClient.invalidateQueries({ queryKey: librariesQueryKey })
      // Jump the user straight to the freshly-imported book so they can
      // verify the metadata landed correctly.
      void navigate({ to: "/book/$id", params: { id: book.id } })
    },
  })

  const rejectMut = useMutation({
    mutationFn: (id: string) => rejectBookDrop(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
    },
  })

  const clearMut = useMutation({
    mutationFn: () => clearProcessedBookDrop(),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
    },
  })

  const error = (approveMut.error ??
    rejectMut.error ??
    clearMut.error) as unknown as ApiError | null

  return (
    <div className="fade-in flex h-full min-h-0 flex-col">
      <TopBar
        title="BookDrop"
        subtitle="Drop files into /bookdrop and they'll appear here for review before joining your library."
        right={
          <div style={{ display: "flex", gap: 8 }}>
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
              disabled={
                approveMut.isPending || active.every((i) => i.state !== "ready")
              }
              onClick={() => {
                for (const item of active) {
                  if (item.state === "ready") {
                    approveMut.mutate({ id: item.id })
                  }
                }
              }}
            >
              <Icon name="check" size={13} /> Approve all ready
            </Button>
          </div>
        }
      />

      <div className="grid min-h-0 flex-1 grid-cols-[440px_minmax(0,1fr)]">
        {/* Left — drop zone + queue */}
        <aside className="flex min-h-0 flex-col overflow-y-auto border-r border-(--color-rule-soft)">
          <div className="px-6 pt-6 pb-2">
            <DropZone
              onUploaded={() => {
                queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
              }}
            />
          </div>

          <RailHeader
            label="In queue"
            count={active.length}
          />
          {queue.isLoading && <RailEmpty>Loading queue…</RailEmpty>}
          {queue.isError && (
            <div className="mx-6 my-3 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink)" style={{ fontSize: 13 }}>
              Failed to load the ingest queue.
            </div>
          )}
          {active.length === 0 && !queue.isLoading && !queue.isError && (
            <RailEmpty>
              Queue is empty. Drop a file into{" "}
              <span className="mono">/bookdrop</span>.
            </RailEmpty>
          )}
          <div className="divide-y divide-(--color-rule-soft) border-y border-(--color-rule-soft)">
            {active.map((f) => (
              <QueueRow
                key={f.id}
                item={f}
                selected={selectedId === f.id}
                onSelect={() => setSelectedId(f.id)}
              />
            ))}
          </div>

          {finished.length > 0 && (
            <>
              <RailHeader
                label="Recently processed"
                count={finished.length}
                action={
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    disabled={clearMut.isPending}
                    title="Remove every imported / rejected row from the queue history"
                    onClick={() => setClearConfirmOpen(true)}
                  >
                    <Icon name="close" size={11} />{" "}
                    {clearMut.isPending ? "Clearing…" : "Clear"}
                  </Button>
                }
              />
              <div className="divide-y divide-(--color-rule-soft) border-y border-(--color-rule-soft)">
                {finished.map((f) => (
                  <QueueRow
                    key={f.id}
                    item={f}
                    selected={selectedId === f.id}
                    onSelect={() => setSelectedId(f.id)}
                  />
                ))}
              </div>
            </>
          )}
        </aside>

        {/* Right — detail */}
        <main className="min-h-0 overflow-y-auto">
          {current ? (
            <div className="mx-auto max-w-[820px] px-10 py-10">
              {error && (
                <div
                  className="mb-6 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink)"
                  style={{ fontSize: 13 }}
                  role="alert"
                >
                  {error.message}
                </div>
              )}

              <div className="mb-8 flex items-baseline justify-between gap-3 border-b border-(--color-rule-soft) pb-3">
                <div>
                  <p className="t-micro mb-2">Review import</p>
                  <h2
                    className="t-h2 wrap-break-word"
                    style={{ fontWeight: 500 }}
                  >
                    {current.title || "Untitled file"}
                  </h2>
                </div>
                <StatusBadge state={current.state} />
              </div>

              <p
                className="mono mb-8 break-all"
                style={{ fontSize: 12, color: "var(--color-ink-3)" }}
              >
                {current.path}
              </p>

              <div className="grid grid-cols-[180px_minmax(0,1fr)] gap-10">
                <CoverPanel item={current} />

                <div className="min-w-0">
                  <dl className="grid grid-cols-2 gap-x-6 gap-y-5">
                    <InfoField
                      label="Title"
                      value={current.title}
                      missing="Could not detect"
                    />
                    <InfoField
                      label="Author"
                      value={current.author}
                      missing="Could not detect"
                    />
                    <InfoField label="Format" value={current.format} mono />
                    <InfoField
                      label="Size"
                      value={formatBytes(current.fileSize)}
                      mono
                    />
                    <InfoField
                      label="Language"
                      value={current.language}
                      mono
                      missing="—"
                    />
                    <InfoField
                      label="State"
                      value={current.state}
                      mono
                    />
                  </dl>

                  {current.description && (
                    <div className="mt-8">
                      <p className="t-label mb-2">Description</p>
                      <p
                        className="font-serif"
                        style={{
                          fontSize: 14.5,
                          lineHeight: 1.6,
                          color: "var(--color-ink-1)",
                          maxWidth: 560,
                          textWrap: "pretty",
                        }}
                      >
                        {current.description}
                      </p>
                    </div>
                  )}

                  {current.state === "failed" && current.errorMsg && (
                    <div
                      className="mt-6 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink)"
                      style={{ fontSize: 13 }}
                    >
                      Processing error: {current.errorMsg}
                    </div>
                  )}

                  <div className="mt-8 border-t border-(--color-rule-soft) pt-6">
                    {current.state === "imported" && current.bookId ? (
                      <Button
                        onClick={() =>
                          void navigate({
                            to: "/book/$id",
                            params: { id: current.bookId! },
                          })
                        }
                      >
                        <Icon name="book-open" size={13} /> Open imported book
                      </Button>
                    ) : current.state === "rejected" ? (
                      <p
                        className="t-small italic"
                        style={{ color: "var(--color-ink-3)" }}
                      >
                        This item was dismissed.
                      </p>
                    ) : (
                      <ApprovalBar
                        item={current}
                        libraries={libraries.data ?? []}
                        disabled={approveMut.isPending || rejectMut.isPending}
                        onApprove={(libraryId) =>
                          approveMut.mutate({ id: current.id, libraryId })
                        }
                        onReject={() => rejectMut.mutate(current.id)}
                      />
                    )}
                  </div>
                </div>
              </div>
            </div>
          ) : (
            <div className="flex h-full items-center justify-center px-8">
              <p
                className="t-small italic"
                style={{ color: "var(--color-ink-3)", maxWidth: 320, textAlign: "center" }}
              >
                Drop a file into the queue to start reviewing.
              </p>
            </div>
          )}
        </main>
      </div>

      <Dialog open={clearConfirmOpen} onOpenChange={setClearConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Clear processed history?</DialogTitle>
            <DialogDescription>
              Remove {finished.length} processed{" "}
              {finished.length === 1 ? "item" : "items"} from the BookDrop queue
              history. Imported books and any files still on disk are not
              affected.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setClearConfirmOpen(false)}
              disabled={clearMut.isPending}
            >
              Cancel
            </Button>
            <Button
              type="button"
              onClick={() =>
                clearMut.mutate(undefined, {
                  onSuccess: () => setClearConfirmOpen(false),
                })
              }
              disabled={clearMut.isPending}
            >
              {clearMut.isPending ? "Clearing…" : "Clear"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function RailHeader({
  label,
  count,
  action,
}: {
  label: string
  count: number
  action?: ReactNode
}) {
  return (
    <div className="flex items-baseline justify-between gap-3 px-6 pt-6 pb-2">
      <div className="flex items-baseline gap-2">
        <span className="t-label">{label}</span>
        <span
          className="mono tabular-nums"
          style={{ fontSize: 11, color: "var(--color-ink-3)" }}
        >
          {count}
        </span>
      </div>
      {action}
    </div>
  )
}

function RailEmpty({ children }: { children: ReactNode }) {
  return (
    <div
      className="t-small italic px-6 py-3"
      style={{ color: "var(--color-ink-3)" }}
    >
      {children}
    </div>
  )
}

function StatusBadge({ state }: { state: BookDropState }) {
  return (
    <span
      className="mono inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap"
      style={{
        fontSize: 10.5,
        letterSpacing: "0.06em",
        textTransform: "uppercase",
        color: "var(--color-ink-2)",
      }}
    >
      <span
        aria-hidden
        style={{
          width: 7,
          height: 7,
          borderRadius: "50%",
          background: STATUS_COLOR[state],
        }}
      />
      {state}
    </span>
  )
}

// Formats accepted by the ingest pipeline (mirrors fileproc.SupportedExts).
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
      // Refresh the queue even if some files errored — successful ones
      // are already in the DB and should surface immediately.
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
        style={{
          padding: "32px 20px",
          border: `1px dashed ${
            dragOver ? "var(--color-editorial-accent)" : "var(--color-rule)"
          }`,
          borderRadius: 3,
          background: dragOver
            ? "var(--color-paper-2)"
            : "transparent",
          textAlign: "center",
          cursor: uploading ? "wait" : "pointer",
          transition:
            "background 160ms cubic-bezier(0.16,1,0.3,1), border-color 160ms",
        }}
      >
        <div
          aria-hidden
          style={{
            display: "inline-flex",
            alignItems: "center",
            justifyContent: "center",
            width: 36,
            height: 36,
            borderRadius: "50%",
            background: dragOver
              ? "var(--color-editorial-accent)"
              : "var(--color-paper-3)",
            color: dragOver
              ? "var(--color-paper-0)"
              : "var(--color-ink-2)",
            transition: "background 160ms, color 160ms",
            marginBottom: 10,
          }}
        >
          <Icon name="upload" size={16} />
        </div>
        <div
          className="font-serif"
          style={{ fontSize: 16, fontWeight: 500, marginBottom: 4 }}
        >
          {uploading
            ? "Uploading…"
            : dragOver
              ? "Drop to queue"
              : "Drop files or click to browse"}
        </div>
        <div
          className="t-small italic"
          style={{ fontSize: 12, color: "var(--color-ink-3)" }}
        >
          EPUB, PDF, CBZ, MOBI, AZW3, FB2, audio
        </div>

        {uploading && (
          <div
            style={{
              marginTop: 14,
              height: 3,
              background: "var(--color-paper-3)",
              borderRadius: 2,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                height: "100%",
                width: `${Math.round(progress * 100)}%`,
                background: "var(--color-editorial-accent)",
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
            // Reset so picking the same file twice still fires change.
            e.target.value = ""
          }}
          style={{ display: "none" }}
        />
      </div>

      {err && (
        <div
          className="mt-2.5 rounded-[3px] border border-(--color-accent-soft) bg-(--color-accent-soft) px-3 py-2 text-(--color-accent-ink)"
          style={{ fontSize: 12.5 }}
          role="alert"
        >
          {err}
        </div>
      )}

      {results && (
        <div
          className="mt-2.5 rounded-[3px] border border-(--color-rule-soft) px-3 py-2"
          style={{ fontSize: 12.5 }}
        >
          <div style={{ marginBottom: failedCount ? 4 : 0 }}>
            {okCount} queued{failedCount ? ` · ${failedCount} failed` : ""}
          </div>
          {results
            .filter((r) => r.error)
            .map((r) => (
              <div
                key={r.filename}
                className="mono"
                style={{ fontSize: 11, color: "var(--color-accent-ink)" }}
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
  selected,
  onSelect,
}: {
  item: BookDropItem
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      className="card-row relative flex w-full items-center gap-3 px-6 py-3 text-left"
      style={{
        background: selected ? "var(--color-paper-2)" : "transparent",
      }}
    >
      {selected && (
        <span
          aria-hidden
          className="absolute inset-y-0 left-0 w-[2px]"
          style={{ background: "var(--color-editorial-accent)" }}
        />
      )}
      <div
        className="mono shrink-0"
        style={{
          width: 36,
          height: 48,
          background: "var(--color-paper-2)",
          border: "1px solid var(--color-rule)",
          borderRadius: 2,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontSize: 9,
          color: "var(--color-ink-3)",
        }}
      >
        {item.format}
      </div>
      <div className="min-w-0 grow">
        <div
          className="mono truncate"
          style={{ fontSize: 11, color: "var(--color-ink-3)" }}
        >
          {item.filename}
        </div>
        <div
          className="truncate font-serif"
          style={{
            fontSize: 13.5,
            fontWeight: 500,
            marginTop: 2,
            fontStyle: item.title ? "normal" : "italic",
            color: item.title ? "var(--color-ink-1)" : "var(--color-ink-3)",
          }}
        >
          {item.title || "Could not detect metadata"}
        </div>
        <div className="mt-1.5 flex items-center gap-2">
          <span
            aria-hidden
            style={{
              width: 6,
              height: 6,
              borderRadius: "50%",
              background: STATUS_COLOR[item.state],
            }}
          />
          <span className="t-micro" style={{ fontSize: 9.5 }}>
            {item.state}
          </span>
          <span
            className="mono"
            style={{ fontSize: 10, color: "var(--color-ink-3)" }}
          >
            {formatBytes(item.fileSize)}
          </span>
        </div>
      </div>
    </button>
  )
}

function CoverPanel({ item }: { item: BookDropItem }) {
  const sharedBox: React.CSSProperties = {
    width: 180,
    height: 270,
    borderRadius: 2,
  }
  if (item.hasCover) {
    return (
      <img
        src={bookdropCoverUrl(item.id)}
        alt=""
        width={180}
        height={270}
        style={{
          ...sharedBox,
          objectFit: "cover",
          boxShadow:
            "inset 0 0 0 1px oklch(0 0 0 / 0.08), 2px 4px 14px oklch(0.2 0.02 60 / 0.16)",
          background: "var(--color-paper-2)",
        }}
      />
    )
  }
  return (
    <div
      className="flex flex-col items-center justify-center"
      style={{
        ...sharedBox,
        background:
          "repeating-linear-gradient(135deg, var(--color-paper-3) 0 8px, var(--color-paper-2) 8px 16px)",
        border: "1px solid var(--color-rule)",
        textAlign: "center",
        padding: 20,
      }}
    >
      <p
        className="font-serif italic"
        style={{
          fontSize: 14,
          color: "var(--color-ink-2)",
          lineHeight: 1.4,
          textWrap: "balance",
        }}
      >
        no cover detected
      </p>
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
    <div className="flex flex-wrap items-center gap-2">
      {libraries.length > 0 && (
        <Select
          value={libraryId}
          onValueChange={(v) => setLibraryId(v || undefined)}
        >
          <SelectTrigger size="sm" className="w-auto">
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
      )}
      <Button
        disabled={disabled || !approvable}
        onClick={() => onApprove(libraryId)}
      >
        <Icon name="check" size={13} /> Approve &amp; add to library
      </Button>
      <Button
        variant="ghost"
        disabled={disabled}
        className="text-(--color-accent-ink) hover:text-(--color-accent-ink) hover:bg-(--color-accent-soft)"
        onClick={onReject}
      >
        Discard file
      </Button>
    </div>
  )
}

function InfoField({
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
    <div className="min-w-0">
      <dt className="t-label mb-1.5">{label}</dt>
      <dd
        className={
          (mono ? "mono " : "font-serif ") + "block min-w-0 wrap-break-word"
        }
        style={{
          fontSize: mono ? 13 : 14.5,
          fontWeight: mono ? 400 : 500,
          color: isMissing ? "var(--color-ink-3)" : "var(--color-ink-1)",
          fontStyle: isMissing ? "italic" : "normal",
        }}
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
