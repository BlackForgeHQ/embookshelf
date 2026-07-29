import { useMemo, useState } from "react"
import { Link } from "@tanstack/react-router"

import type { BookDropItem } from "@/api/bookdrop"
import {
  BOOKDROP_STATE_LABEL,
  bookdropFilesQuery,
  bookdropQuery,
  clearProcessedBookDrop,
  isTerminalState,
  wipeBookDropFiles,
} from "@/api/bookdrop"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { ConfirmPhraseDialog } from "@/components/ConfirmPhraseDialog"
import {
  Card,
  DefRow,
  InboxMark,
  NotebookEmpty,
} from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

export function BookDropPanel() {
  const queue = useApiQuery(bookdropQuery)

  const files = useApiQuery(bookdropFilesQuery)

  const processed = useMemo(
    () =>
      (queue.data ?? [])
        .filter((i) => isTerminalState(i.state))
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt)),
    [queue.data]
  )
  const processedCount = processed.length

  const [clearOpen, setClearOpen] = useState(false)
  const [wipeOpen, setWipeOpen] = useState(false)

  const clearMut = useApiMutation(clearProcessedBookDrop, {
    successToast: (n) => `Cleared ${n} processed item${n === 1 ? "" : "s"}.`,
    errorToast: (err) => err.message || "Failed to clear processed history.",
    onSuccess: () => setClearOpen(false),
  })

  const wipeMut = useApiMutation(wipeBookDropFiles, {
    successToast: (res) =>
      `Wiped ${res.deleted} file${res.deleted === 1 ? "" : "s"} (${formatBytes(res.freed)}).`,
    errorToast: (err) => err.message || "Failed to wipe BookDrop.",
    onSuccess: () => setWipeOpen(false),
  })

  return (
    <>
      <h2 className="t-h2" style={{ marginBottom: 8 }}>
        BookDrop
      </h2>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Housekeeping for the staging directory. Clear processed history wipes
        terminal-state queue rows. Wipe files removes every file under{" "}
        <span className="mono">BOOKDROP_PATH</span> on disk — files referenced
        by an in-flight extraction are left alone.
      </p>

      <Card>
        <DefRow
          label="Processed queue rows"
          value={
            <span className="mono">
              {queue.isLoading ? "…" : processedCount}
            </span>
          }
        />
        <DefRow
          label="Files on disk"
          value={
            <span className="mono">
              {files.isLoading
                ? "…"
                : `${files.data?.count ?? 0} (${formatBytes(files.data?.bytes ?? 0)})`}
            </span>
          }
        />
        <DefRow
          label="In-flight (skipped by wipe)"
          value={
            <span className="mono">
              {files.isLoading ? "…" : (files.data?.skippedInFlight ?? 0)}
            </span>
          }
        />
      </Card>

      <div
        style={{
          display: "flex",
          gap: 8,
          marginTop: 16,
          flexWrap: "wrap",
        }}
      >
        <Button
          type="button"
          variant="outline"
          disabled={processedCount === 0 || clearMut.isPending}
          onClick={() => setClearOpen(true)}
        >
          Clear processed history
        </Button>
        <Button
          type="button"
          variant="destructive"
          disabled={(files.data?.count ?? 0) === 0 || wipeMut.isPending}
          onClick={() => setWipeOpen(true)}
        >
          Wipe all files
        </Button>
      </div>

      <h3 className="t-h3" style={{ marginTop: 32, marginBottom: 8 }}>
        Recently processed
      </h3>
      <p className="t-small" style={{ marginBottom: 12, fontStyle: "italic" }}>
        Imported and discarded items. Use <em>Clear processed history</em> above
        to empty this list.
      </p>
      {queue.isLoading ? (
        <Card>
          <p className="t-small text-muted-foreground italic">Loading…</p>
        </Card>
      ) : processed.length === 0 ? (
        <NotebookEmpty
          mark={<InboxMark />}
          title="No processed items yet."
          body="Imported and discarded BookDrop items will land here. Drop a file into the watched folder to populate the list."
        />
      ) : (
        <Card>
          <ul className="flex flex-col">
            {processed.map((item) => (
              <ProcessedRow key={item.id} item={item} />
            ))}
          </ul>
        </Card>
      )}

      <ClearProcessedDialog
        open={clearOpen}
        onOpenChange={setClearOpen}
        count={processedCount}
        busy={clearMut.isPending}
        onConfirm={() => clearMut.mutate()}
      />

      <WipeFilesDialog
        open={wipeOpen}
        onOpenChange={setWipeOpen}
        count={files.data?.count ?? 0}
        bytes={files.data?.bytes ?? 0}
        skippedInFlight={files.data?.skippedInFlight ?? 0}
        busy={wipeMut.isPending}
        onConfirm={() => wipeMut.mutate()}
      />
    </>
  )
}

function ProcessedRow({ item }: { item: BookDropItem }) {
  const date = new Date(item.updatedAt)
  const dateLabel = Number.isNaN(date.getTime())
    ? "—"
    : date.toLocaleString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      })
  const title = item.title?.trim() || item.filename
  const isImported = item.state === "imported"
  // The shared map, not a third spelling of the same two words: this
  // panel used to render "Imported"/"Discarded" while the queue route
  // rendered "imported"/"discarded" from the map (#213).
  const stateLabel = BOOKDROP_STATE_LABEL[item.state]
  return (
    <li className="flex items-center gap-3 border-t border-dashed border-border py-2 first:border-0">
      <span
        className="mono w-12 shrink-0 text-[10.5px] tracking-wide text-muted-foreground uppercase"
        title={item.format}
      >
        {item.format}
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13.5px]">{title}</div>
        <div className="t-small text-muted-foreground">
          <span
            className={`capitalize ${
              isImported
                ? "text-(--color-editorial-accent)"
                : "text-(--color-accent-ink)"
            }`}
          >
            {stateLabel}
          </span>
          {item.author ? <> · {item.author}</> : null} · {dateLabel}
        </div>
      </div>
      {isImported && item.bookId ? (
        <Link
          to="/book/$id"
          params={{ id: item.bookId }}
          className="t-small shrink-0 text-muted-foreground underline hover:text-foreground"
        >
          Open
        </Link>
      ) : null}
    </li>
  )
}

function ClearProcessedDialog({
  open,
  onOpenChange,
  count,
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  count: number
  busy: boolean
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Clear processed history?</DialogTitle>
          <DialogDescription>
            Remove {count} processed {count === 1 ? "item" : "items"} from the
            BookDrop queue history. Imported books and any files still on disk
            are not affected.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button type="button" onClick={onConfirm} disabled={busy}>
            {busy ? "Clearing…" : "Clear"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// The token is `bookdrop` rather than e.g. `WIPE` so the admin must
// register what they're wiping, not just type a generic destructive verb.
// See ADR-0014.
const WIPE_PHRASE = "bookdrop"

// WipeFilesDialog is the ConfirmPhraseDialog with the wipe's consequences
// filled in; the gate itself lives in that module.
function WipeFilesDialog({
  open,
  onOpenChange,
  count,
  bytes,
  skippedInFlight,
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  count: number
  bytes: number
  skippedInFlight: number
  busy: boolean
  onConfirm: () => void
}) {
  return (
    <ConfirmPhraseDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Wipe BookDrop files?"
      description={
        <>
          Permanently deletes <strong>{count}</strong>{" "}
          {count === 1 ? "file" : "files"} ({formatBytes(bytes)}) under{" "}
          <span className="mono">BOOKDROP_PATH</span>. Files from other users'
          pending uploads are included.
          {skippedInFlight > 0 && (
            <>
              {" "}
              <strong>{skippedInFlight}</strong> in-flight{" "}
              {skippedInFlight === 1 ? "file" : "files"} will be left alone.
            </>
          )}
        </>
      }
      phrase={WIPE_PHRASE}
      confirmLabel="Wipe files"
      busyLabel="Wiping…"
      busy={busy}
      onConfirm={onConfirm}
    />
  )
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B"
  const units = ["B", "KB", "MB", "GB"]
  let value = n
  let u = 0
  while (value >= 1024 && u < units.length - 1) {
    value /= 1024
    u++
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[u]}`
}
