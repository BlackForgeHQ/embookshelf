import { useEffect, useMemo, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import {
  bookdropQueryKey,
  clearProcessedBookDrop,
  fetchBookDrop,
  previewBookDropFiles,
  wipeBookDropFiles,
} from "@/api/bookdrop"
import { AdminGate, Card, DefRow } from "@/components/SettingsShared"
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
import { Label } from "@/components/ui/label"

const bookdropFilesQueryKey = ["settings", "bookdrop", "files"] as const

export function BookDropPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient()

  const queue = useQuery({
    queryKey: bookdropQueryKey,
    queryFn: fetchBookDrop,
    enabled: isAdmin,
  })

  const files = useQuery({
    queryKey: bookdropFilesQueryKey,
    queryFn: previewBookDropFiles,
    enabled: isAdmin,
  })

  const processedCount = useMemo(
    () =>
      (queue.data ?? []).filter(
        (i) => i.state === "imported" || i.state === "rejected"
      ).length,
    [queue.data]
  )

  const [clearOpen, setClearOpen] = useState(false)
  const [wipeOpen, setWipeOpen] = useState(false)

  const clearMut = useMutation({
    mutationFn: clearProcessedBookDrop,
    onSuccess: (n) => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      toast.success(`Cleared ${n} processed item${n === 1 ? "" : "s"}.`)
      setClearOpen(false)
    },
    onError: (err: ApiError) => {
      toast.error(err.message || "Failed to clear processed history.")
    },
  })

  const wipeMut = useMutation({
    mutationFn: wipeBookDropFiles,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: bookdropQueryKey })
      queryClient.invalidateQueries({ queryKey: bookdropFilesQueryKey })
      toast.success(
        `Wiped ${res.deleted} file${res.deleted === 1 ? "" : "s"} (${formatBytes(res.freed)}).`
      )
      setWipeOpen(false)
    },
    onError: (err: ApiError) => {
      toast.error(err.message || "Failed to wipe BookDrop.")
    },
  })

  if (!isAdmin) return <AdminGate label="BookDrop" />

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

// WipeFilesDialog mirrors DeleteLibraryDialog: type the literal token to
// enable confirm. The token is `bookdrop` rather than e.g. `WIPE` so the
// admin must register what they're wiping, not just type a generic
// destructive verb. See ADR-0014.
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
  const [confirmInput, setConfirmInput] = useState("")

  useEffect(() => {
    if (!open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setConfirmInput("")
    }
  }, [open])

  const matches = confirmInput.trim() === "bookdrop"

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>Wipe BookDrop files?</DialogTitle>
          <DialogDescription>
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
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="wipe-confirm">
            Type <span className="mono">bookdrop</span> to confirm.
          </Label>
          <Input
            id="wipe-confirm"
            value={confirmInput}
            onChange={(e) => setConfirmInput(e.target.value)}
            autoFocus
          />
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            Cancel
          </Button>
          <Button
            type="button"
            variant="destructive"
            onClick={onConfirm}
            disabled={!matches || busy}
          >
            {busy ? "Wiping…" : "Wipe files"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
