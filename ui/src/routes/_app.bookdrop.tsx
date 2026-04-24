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
import { Input } from "@/components/ui/input"
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

// Dot color mapped to the state lifecycle. Terminal states keep using the
// non-terminal palette so the user sees "this was ready / failed" context
// before they clear the row.
const STATUS_COLOR: Record<BookDropState, string> = {
  ready: "oklch(0.58 0.12 140)",
  processing: "oklch(0.65 0.09 80)",
  discovered: "var(--color-ink-4)",
  failed: "var(--color-accent)",
  imported: "var(--color-ink-3)",
  rejected: "var(--color-ink-4)",
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
    (queue.data ?? []).find((i) => i.id === selectedId) ??
    active[0] ??
    finished[0]
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
    <div className="fade-in">
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

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "440px 1fr",
          flex: 1,
          minHeight: 0,
        }}
      >
        {/* Left — file list */}
        <div
          style={{
            borderRight: "1px solid var(--color-rule-soft)",
            overflow: "auto",
          }}
        >
          <DropZone
            onUploaded={() => {
              queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
            }}
          />

          <div className="t-label" style={{ padding: "4px 20px 10px" }}>
            In queue · {active.length}
          </div>
          {queue.isLoading && (
            <div
              className="t-small"
              style={{ padding: "12px 20px", fontStyle: "italic" }}
            >
              Loading queue…
            </div>
          )}
          {queue.isError && (
            <div
              className="flash error"
              style={{
                margin: 20,
                padding: "10px 14px",
                borderRadius: 2,
                fontSize: 13,
              }}
            >
              Failed to load the ingest queue.
            </div>
          )}
          {active.length === 0 && !queue.isLoading && !queue.isError && (
            <div
              className="t-small"
              style={{ padding: "12px 20px", fontStyle: "italic" }}
            >
              Queue is empty. Drop a file into{" "}
              <span className="mono">/bookdrop</span>.
            </div>
          )}
          {active.map((f) => (
            <QueueRow
              key={f.id}
              item={f}
              selected={selectedId === f.id}
              onSelect={() => setSelectedId(f.id)}
            />
          ))}

          {finished.length > 0 && (
            <>
              <div
                style={{
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "space-between",
                  padding: "16px 20px 10px",
                }}
              >
                <span className="t-label">
                  Recently processed · {finished.length}
                </span>
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
              </div>
              {finished.map((f) => (
                <QueueRow
                  key={f.id}
                  item={f}
                  selected={selectedId === f.id}
                  onSelect={() => setSelectedId(f.id)}
                />
              ))}
            </>
          )}
        </div>

        {/* Right — detail */}
        {current && (
          <div style={{ overflow: "auto", padding: "32px 40px" }}>
            {error && (
              <div
                className="flash error"
                style={{
                  padding: "10px 14px",
                  border: "1px solid var(--color-accent-soft)",
                  background: "var(--color-accent-soft)",
                  color: "var(--color-accent-ink)",
                  borderRadius: 2,
                  fontSize: 13,
                  marginBottom: 20,
                }}
              >
                {error.message}
              </div>
            )}

            <div className="t-label" style={{ marginBottom: 6 }}>
              Review import
            </div>
            <div
              className="mono"
              style={{
                fontSize: 12,
                color: "var(--color-ink-3)",
                marginBottom: 20,
              }}
            >
              {current.path}
            </div>

            <div
              style={{
                display: "grid",
                gridTemplateColumns: "160px 1fr",
                gap: 32,
              }}
            >
              <CoverPanel item={current} />
              <div>
                <div
                  style={{
                    display: "grid",
                    gridTemplateColumns: "1fr 1fr",
                    gap: 14,
                    marginBottom: 20,
                  }}
                >
                  <Field
                    label="Title"
                    value={current.title ?? ""}
                    placeholder="Could not detect"
                  />
                  <Field
                    label="Author"
                    value={current.author ?? ""}
                    placeholder="Could not detect"
                  />
                  <Field label="Format" value={current.format} readOnly />
                  <Field
                    label="Size"
                    value={formatBytes(current.fileSize)}
                    readOnly
                  />
                  <Field
                    label="Language"
                    value={current.language ?? ""}
                    placeholder="—"
                  />
                  <Field label="State" value={current.state} readOnly />
                </div>

                {current.description && (
                  <div style={{ marginBottom: 20 }}>
                    <div className="t-label" style={{ marginBottom: 6 }}>
                      Description
                    </div>
                    <p
                      style={{
                        fontSize: 13.5,
                        lineHeight: 1.55,
                        color: "var(--color-ink-1)",
                        maxWidth: 560,
                      }}
                    >
                      {current.description}
                    </p>
                  </div>
                )}

                {current.state === "failed" && current.errorMsg && (
                  <div
                    style={{
                      padding: 12,
                      marginBottom: 20,
                      border: "1px solid var(--color-accent-soft)",
                      background: "var(--color-accent-soft)",
                      color: "var(--color-accent-ink)",
                      fontSize: 13,
                      borderRadius: 2,
                    }}
                  >
                    Processing error: {current.errorMsg}
                  </div>
                )}

                {current.state === "imported" && current.bookId ? (
                  <div
                    style={{
                      display: "flex",
                      gap: 8,
                      borderTop: "1px solid var(--color-rule-soft)",
                      paddingTop: 20,
                    }}
                  >
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
                  </div>
                ) : current.state === "rejected" ? (
                  <div
                    className="t-small"
                    style={{ fontStyle: "italic", color: "var(--color-ink-3)" }}
                  >
                    This item was dismissed.
                  </div>
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
        )}
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
      setErr((e as { message?: string })?.message ?? "upload failed")
    } finally {
      setUploading(false)
      setProgress(0)
    }
  }

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const files = Array.from(e.dataTransfer.files ?? [])
    void upload(files)
  }

  const failedCount = results?.filter((r) => r.error).length ?? 0
  const okCount = results?.filter((r) => r.item).length ?? 0

  return (
    <div style={{ margin: 20 }}>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDragOver(true)
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={onDrop}
        onClick={() => inputRef.current?.click()}
        style={{
          padding: "28px 16px",
          border: `2px dashed ${dragOver ? "var(--color-accent)" : "var(--color-rule)"}`,
          borderRadius: 3,
          background: dragOver
            ? "var(--color-paper-3)"
            : "var(--color-paper-2)",
          textAlign: "center",
          cursor: uploading ? "wait" : "pointer",
          transition: "background 120ms, border-color 120ms",
        }}
      >
        <Icon name="upload" size={20} className="mono" />
        <div style={{ fontSize: 14, fontWeight: 500, marginTop: 8 }}>
          {uploading ? "Uploading…" : "Drop files here or click to browse"}
        </div>
        <div className="t-small" style={{ fontSize: 12 }}>
          Multi-file supported · EPUB, PDF, CBZ, MOBI, AZW3, FB2, audio
        </div>

        {uploading && (
          <div
            style={{
              marginTop: 14,
              height: 4,
              background: "var(--color-paper-0)",
              borderRadius: 2,
              overflow: "hidden",
            }}
          >
            <div
              style={{
                height: "100%",
                width: `${Math.round(progress * 100)}%`,
                background: "var(--color-accent)",
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
          className="flash error"
          style={{
            marginTop: 10,
            padding: "8px 12px",
            border: "1px solid var(--color-accent-soft)",
            background: "var(--color-accent-soft)",
            color: "var(--color-accent-ink)",
            borderRadius: 2,
            fontSize: 12.5,
          }}
        >
          {err}
        </div>
      )}

      {results && (
        <div
          style={{
            marginTop: 10,
            padding: "8px 12px",
            background: "var(--color-paper-0)",
            border: "1px solid var(--color-rule-soft)",
            borderRadius: 2,
            fontSize: 12.5,
          }}
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
    <div
      onClick={onSelect}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "12px 20px",
        cursor: "pointer",
        borderBottom: "1px solid var(--color-rule-soft)",
        background: selected ? "var(--color-paper-3)" : "transparent",
        borderLeft: selected
          ? "2px solid var(--color-accent)"
          : "2px solid transparent",
      }}
    >
      <div
        style={{
          width: 36,
          height: 48,
          background: "var(--color-paper-2)",
          border: "1px solid var(--color-rule)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontFamily: "var(--font-mono)",
          fontSize: 9,
          color: "var(--color-ink-3)",
          flexShrink: 0,
        }}
      >
        {item.format}
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div
          className="mono"
          style={{
            fontSize: 11,
            color: "var(--color-ink-2)",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {item.filename}
        </div>
        <div
          style={{
            fontSize: 13,
            fontWeight: 500,
            marginTop: 2,
            fontStyle: item.title ? "normal" : "italic",
            color: item.title ? "var(--color-ink-1)" : "var(--color-ink-3)",
          }}
        >
          {item.title || "Could not detect metadata"}
        </div>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 8,
            marginTop: 4,
          }}
        >
          <span
            style={{
              width: 6,
              height: 6,
              borderRadius: "50%",
              background: STATUS_COLOR[item.state] ?? "var(--color-ink-4)",
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
    </div>
  )
}

function CoverPanel({ item }: { item: BookDropItem }) {
  if (item.hasCover) {
    return (
      <div>
        <img
          src={bookdropCoverUrl(item.id)}
          alt=""
          width={160}
          height={240}
          style={{
            width: 160,
            height: 240,
            objectFit: "cover",
            boxShadow: "2px 4px 12px oklch(0.2 0.02 60 / 0.15)",
            background: "var(--color-paper-2)",
          }}
        />
      </div>
    )
  }
  return (
    <div>
      <div
        style={{
          width: 160,
          height: 240,
          background:
            "repeating-linear-gradient(135deg, var(--color-paper-3) 0 8px, var(--color-paper-2) 8px 16px)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          border: "1px solid var(--color-rule)",
        }}
      >
        <div
          className="t-micro"
          style={{ textAlign: "center", lineHeight: 1.4 }}
        >
          no cover
          <br />
          detected
        </div>
      </div>
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
    <div
      style={{
        display: "flex",
        gap: 8,
        alignItems: "center",
        borderTop: "1px solid var(--color-rule-soft)",
        paddingTop: 20,
        flexWrap: "wrap",
      }}
    >
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
        className="text-(--color-accent-ink)"
        onClick={onReject}
      >
        Discard file
      </Button>
    </div>
  )
}

type FieldProps = {
  label: string
  value: string
  placeholder?: string
  readOnly?: boolean
}

function Field({ label, value, placeholder, readOnly }: FieldProps): ReactNode {
  return (
    <div>
      <div className="t-label" style={{ marginBottom: 6 }}>
        {label}
      </div>
      <Input
        defaultValue={value}
        placeholder={placeholder}
        readOnly={readOnly}
        className={readOnly ? "bg-muted text-muted-foreground" : undefined}
      />
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
