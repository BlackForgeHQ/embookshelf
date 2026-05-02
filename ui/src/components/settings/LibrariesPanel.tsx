import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type {
  LibraryKind,
  SettingsLibrary,
} from "@/api/settings"
import {
  appConfigQueryKey,
  createLibrary,
  deleteLibrary,
  fetchAppConfig,
  fetchSettingsLibraries,
  rescanLibrary,
  settingsLibrariesQueryKey,
} from "@/api/settings"
import { Icon } from "@/components/Icon"
import {
  AdminGate,
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { slugify } from "@/lib/utils"

export function LibrariesPanel({ isAdmin }: { isAdmin: boolean }) {
  const queryClient = useQueryClient()
  const [creatorOpen, setCreatorOpen] = useState(false)

  const libraries = useQuery({
    queryKey: settingsLibrariesQueryKey,
    queryFn: fetchSettingsLibraries,
    enabled: isAdmin,
  })

  const appConfig = useQuery({
    queryKey: appConfigQueryKey,
    queryFn: fetchAppConfig,
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: settingsLibrariesQueryKey })
    queryClient.invalidateQueries({ queryKey: ["libraries"] })
  }

  const rescanMut = useMutation({
    mutationFn: (id: string) => rescanLibrary(id),
    onSuccess: () => {
      invalidate()
      toast.success("Rescan started.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })
  const deleteLibraryMut = useMutation({
    mutationFn: ({ id, purge }: { id: string; purge: boolean }) =>
      deleteLibrary(id, { purge }),
    onSuccess: () => {
      invalidate()
      toast.success("Library removed.")
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  if (!isAdmin) return <AdminGate label="Libraries" />

  const existingNames = (libraries.data ?? []).map((l) => l.name)

  return (
    <>
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: 12,
          marginBottom: 8,
        }}
      >
        <h2 className="t-h2">Libraries</h2>
        <div className="grow" />
        <Button variant="outline" onClick={() => setCreatorOpen(true)}>
          <Icon name="plus" size={13} /> New library
        </Button>
      </div>
      <p className="t-small" style={{ marginBottom: 24, fontStyle: "italic" }}>
        Each library owns one filesystem root, fixed at creation. Approved books
        are moved under that root using the library&apos;s file-naming pattern —
        scans discover new files and enqueue them through the BookDrop review
        queue.
      </p>

      {libraries.isLoading && (
        <div className="t-small" style={{ fontStyle: "italic" }}>
          Loading libraries…
        </div>
      )}

      {(libraries.data ?? []).map((lib) => (
        <LibraryCard
          key={lib.id}
          library={lib}
          rescanBusy={rescanMut.isPending}
          deleteBusy={deleteLibraryMut.isPending}
          onRescan={() => rescanMut.mutate(lib.id)}
          onDeleteLibrary={(purge) =>
            deleteLibraryMut.mutate({ id: lib.id, purge })
          }
        />
      ))}

      <LibraryCreatorDialog
        open={creatorOpen}
        onOpenChange={setCreatorOpen}
        existingNames={existingNames}
        s3Available={appConfig.data?.s3Available ?? false}
        onCreated={() => {
          invalidate()
          setCreatorOpen(false)
        }}
      />
    </>
  )
}

type LibraryCreatorDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  existingNames: Array<string>
  s3Available: boolean
  onCreated: () => void
}

// Modeled after spec/library-creation.spec.md §3 + §4. Embookshelf's library
// model is simpler than BookLore's (no icon/watch/format policy yet), so the
// form collapses to name + kind selector + paths + an opt-in "scan immediately"
// toggle.
function LibraryCreatorDialog({
  open,
  onOpenChange,
  existingNames,
  s3Available,
  onCreated,
}: LibraryCreatorDialogProps) {
  const [name, setName] = useState("")
  const [kind, setKind] = useState<LibraryKind>("local")
  const [scanOnCreate, setScanOnCreate] = useState(true)

  // Reset local state whenever the dialog closes so re-opening is a blank slate.
  useEffect(() => {
    if (open) return
    // Prop→state sync on close; not a cascading render.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setName("")
    setKind("local")
    setScanOnCreate(true)
  }, [open])

  const trimmedName = name.trim()
  const nameCollision = existingNames.some(
    (existing) => existing.toLowerCase() === trimmedName.toLowerCase()
  )
  const nameValid = trimmedName !== "" && !nameCollision

  // Path is server-derived for both kinds (per ADR 0002 for local; existing
  // convention for s3). The UI just previews the slug.
  const slug = trimmedName !== "" ? slugify(trimmedName) : ""
  const derivedPrefix =
    kind === "s3" && slug !== "" ? `libraries/${slug}/` : null

  const createMut = useMutation({
    mutationFn: () =>
      createLibrary({
        name: trimmedName,
        kind,
        scan: scanOnCreate,
      }),
    onSuccess: () => {
      toast.success("Library created.")
      onCreated()
    },
    onError: (e) => toast.error((e as unknown as ApiError).message),
  })

  const submitDisabled = !nameValid || createMut.isPending

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[520px]">
        <DialogHeader>
          <DialogTitle>New library</DialogTitle>
          <DialogDescription>
            Create a new library backed by a local filesystem folder or an S3
            bucket prefix. The storage choice is fixed at creation time.
          </DialogDescription>
        </DialogHeader>

        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          <div>
            <Label
              htmlFor="lib-name"
              style={{ display: "block", marginBottom: 6 }}
            >
              Name
            </Label>
            <Input
              id="lib-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Fiction"
              autoFocus
            />
            {trimmedName !== "" && nameCollision && (
              <div
                className="t-small"
                style={{ color: "var(--color-accent-ink)", marginTop: 6 }}
              >
                A library with that name already exists.
              </div>
            )}
          </div>

          <div>
            <Label style={{ display: "block", marginBottom: 6 }}>Storage</Label>
            <div className="flex flex-col sm:flex-row gap-2 sm:gap-4">
              <button
                type="button"
                onClick={() => setKind("local")}
                style={{
                  flex: 1,
                  padding: "8px 12px",
                  border: `1px solid ${kind === "local" ? "var(--color-accent)" : "var(--color-rule-soft)"}`,
                  borderRadius: 4,
                  background:
                    kind === "local"
                      ? "var(--color-accent-subtle)"
                      : "var(--color-paper-0)",
                  cursor: "pointer",
                  textAlign: "left" as const,
                }}
              >
                <div className="t-small" style={{ fontWeight: 500 }}>
                  Local filesystem
                </div>
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground" style={{ fontStyle: "italic" }}>
                  Books stored on disk
                </div>
              </button>
              <button
                type="button"
                onClick={() => s3Available && setKind("s3")}
                disabled={!s3Available}
                title={
                  s3Available ? undefined : "Set EMBOOKSHELF_S3_BUCKET to enable"
                }
                style={{
                  flex: 1,
                  padding: "8px 12px",
                  border: `1px solid ${kind === "s3" ? "var(--color-accent)" : "var(--color-rule-soft)"}`,
                  borderRadius: 4,
                  background:
                    kind === "s3"
                      ? "var(--color-accent-subtle)"
                      : "var(--color-paper-0)",
                  cursor: s3Available ? "pointer" : "not-allowed",
                  opacity: s3Available ? 1 : 0.5,
                  textAlign: "left" as const,
                }}
              >
                <div className="t-small" style={{ fontWeight: 500 }}>
                  S3 bucket
                </div>
                <div className="text-[10px] uppercase tracking-wider text-muted-foreground" style={{ fontStyle: "italic" }}>
                  {s3Available
                    ? "Books stored in shared bucket"
                    : "EMBOOKSHELF_S3_BUCKET not set"}
                </div>
              </button>
            </div>
          </div>

          {kind === "local" ? (
            <div>
              <Label style={{ display: "block", marginBottom: 6 }}>
                Folder (auto-derived)
              </Label>
              <p className="t-small italic text-(--color-ink-3)">
                Will be created at{" "}
                <span className="mono">{`(data path)/libraries/${slug || "<slug>"}/`}</span>
              </p>
              <div
                className="text-[10px] uppercase tracking-wider text-muted-foreground"
                style={{ marginTop: 6, fontStyle: "italic" }}
              >
                The folder is created under the configured DATA_PATH and cannot
                be changed later. See ADR 0002 (managed local library folders).
              </div>
            </div>
          ) : (
            <div>
              <Label style={{ display: "block", marginBottom: 6 }}>
                Bucket prefix (auto-derived)
              </Label>
              <div
                className="mono"
                style={{
                  padding: "8px 12px",
                  background: "var(--color-paper-2)",
                  borderRadius: 4,
                  fontSize: 12.5,
                  color: derivedPrefix
                    ? "inherit"
                    : "var(--color-ink-3)",
                  fontStyle: derivedPrefix ? "normal" : "italic",
                }}
              >
                {derivedPrefix ?? "Enter a name above to preview"}
              </div>
              <div
                className="text-[10px] uppercase tracking-wider text-muted-foreground"
                style={{ marginTop: 6, fontStyle: "italic" }}
              >
                Prefix is derived from the library name and cannot be changed
                later. Files sync to this prefix in the shared bucket.
              </div>
            </div>
          )}

          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              cursor: "pointer",
            }}
          >
            <Switch
              checked={scanOnCreate}
              onCheckedChange={(v) => setScanOnCreate(Boolean(v))}
            />
            <span className="t-small">Scan immediately after creating</span>
          </label>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={createMut.isPending}
          >
            Cancel
          </Button>
          <Button
            type="button"
            onClick={() => createMut.mutate()}
            disabled={submitDisabled}
          >
            {createMut.isPending ? "Creating…" : "Create library"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type LibraryCardProps = {
  library: SettingsLibrary
  rescanBusy: boolean
  deleteBusy: boolean
  onRescan: () => void
  onDeleteLibrary: (purge: boolean) => void
}

// isS3Library is a heuristic: s3 libraries have an empty path but a
// non-empty backend_id. The SettingsLibrary DTO doesn't expose backend_id
// directly, but we can infer it from the empty path.
function isS3Library(lib: SettingsLibrary) {
  return lib.path === ""
}

function LibraryCard({
  library,
  rescanBusy,
  deleteBusy,
  onRescan,
  onDeleteLibrary,
}: LibraryCardProps) {
  const [confirmOpen, setConfirmOpen] = useState(false)

  const lastScanned = library.lastScannedAt
    ? new Date(library.lastScannedAt).toLocaleString()
    : null

  return (
    <div
      className="flex flex-col gap-4 p-5 mb-5 bg-card border border-border rounded-lg shadow-sm"
    >
      <div
        className="flex items-baseline gap-3 mb-1"
      >
        <div>
          <div className="text-[15px] font-medium truncate">{library.name}</div>
          <div
            className="font-mono text-[11px] text-muted-foreground truncate max-w-full"
          >
            /{library.slug}
          </div>
        </div>
        <div className="grow" />
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground">{library.bookCount} volumes</span>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          title="Delete library"
          aria-label={`Delete ${library.name}`}
          disabled={deleteBusy}
          onClick={() => setConfirmOpen(true)}
          className="text-destructive hover:bg-destructive/10"
        >
          <Icon name="close" size={12} />
        </Button>
      </div>

      <DeleteLibraryDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        library={library}
        isS3={isS3Library(library)}
        busy={deleteBusy}
        onConfirm={(purge) => {
          onDeleteLibrary(purge)
          setConfirmOpen(false)
        }}
      />

      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          padding: "10px 12px",
          background: "var(--color-paper-2)",
          borderRadius: 2,
          marginBottom: 14,
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="t-label" style={{ marginBottom: 4 }}>
            Folder (fixed)
          </div>
          <div
            className="mono"
            style={{ fontSize: 12.5, wordBreak: "break-all" }}
          >
            {library.path || <em>(empty)</em>}
          </div>
          <div
            className="text-[10px] uppercase tracking-wider text-muted-foreground"
            style={{ marginTop: 4, fontStyle: "italic" }}
          >
            {lastScanned
              ? `last scan ${lastScanned} · ${library.fileCount.toLocaleString()} files, ${library.discoveredCount.toLocaleString()} discovered`
              : "never scanned"}
          </div>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRescan}
          disabled={rescanBusy || library.path === ""}
        >
          <Icon name="refresh" size={13} />{" "}
          {rescanBusy ? "Scanning…" : "Rescan"}
        </Button>
      </div>

      <p className="t-small" style={{ fontStyle: "italic", marginTop: 0 }}>
        File naming patterns are managed in{" "}
        <strong>File naming patterns</strong>.
      </p>
    </div>
  )
}

// DeleteLibraryDialog confirms a destructive library teardown. The
// "type the name to confirm" gate matches the weight of the action —
// cascading DELETE takes every book, annotation, reading session, and
// shelf assignment inside the library with it. Source files on disk
// are left alone (user-managed roots), so re-scanning the same path
// under a fresh library is how an admin undoes a mistake.
function DeleteLibraryDialog({
  open,
  onOpenChange,
  library,
  isS3,
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  library: SettingsLibrary
  isS3: boolean
  busy: boolean
  onConfirm: (purge: boolean) => void
}) {
  const [confirmInput, setConfirmInput] = useState("")
  const [purge, setPurge] = useState(false)

  useEffect(() => {
    // Reset the typed confirmation and purge flag on close — prop→state sync.
    if (!open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setConfirmInput("")
      setPurge(false)
    }
  }, [open])

  const matches = confirmInput.trim() === library.name

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[440px]">
        <DialogHeader>
          <DialogTitle>Delete library</DialogTitle>
          <DialogDescription>
            This removes <strong>{library.name}</strong>, all{" "}
            {library.bookCount} book records, their cover images, and every
            annotation, reading session, and shelf assignment inside it.{" "}
            {isS3
              ? "S3 objects are left alone unless you check the purge option below."
              : "Source files on disk are left alone."}
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-2">
          <Label htmlFor="delete-confirm">
            Type <span className="mono">{library.name}</span> to confirm.
          </Label>
          <Input
            id="delete-confirm"
            value={confirmInput}
            onChange={(e) => setConfirmInput(e.target.value)}
            autoFocus
          />
        </div>

        {isS3 && (
          <label
            style={{
              display: "flex",
              alignItems: "center",
              gap: 10,
              cursor: "pointer",
            }}
          >
            <Switch checked={purge} onCheckedChange={(v) => setPurge(Boolean(v))} />
            <span className="t-small">
              Also delete all files in the S3 bucket prefix
            </span>
          </label>
        )}

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
            onClick={() => onConfirm(purge)}
            disabled={!matches || busy}
          >
            {busy ? "Deleting…" : "Delete library"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// Metadata providers
// ---------------------------------------------------------------------------
