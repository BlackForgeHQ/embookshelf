import { useEffect, useMemo, useState } from "react"

import type { LibraryKind, SettingsLibrary } from "@/api/settings"
import {
  appConfigQuery,
  createLibrary,
  deleteLibrary,
  rescanLibrary,
  settingsLibrariesQuery,
} from "@/api/settings"
import { useApiMutation } from "@/api/mutation"
import { useApiQuery } from "@/api/query"
import { ConfirmPhraseDialog } from "@/components/ConfirmPhraseDialog"
import { Icon } from "@/components/Icon"
import { NotebookEmpty } from "@/components/SettingsShared"
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
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { slugify } from "@/lib/utils"

export function LibrariesPanel() {
  const [creatorOpen, setCreatorOpen] = useState(false)

  const libraries = useApiQuery(settingsLibrariesQuery)

  const appConfig = useApiQuery(appConfigQuery)

  const rescanMut = useApiMutation(rescanLibrary, {
    successToast: "Rescan started.",
  })
  const deleteLibraryMut = useApiMutation(deleteLibrary, {
    successToast: "Library removed.",
  })

  const rows = useMemo(() => libraries.data ?? [], [libraries.data])
  const existingNames = useMemo(() => rows.map((l) => l.name), [rows])

  const isEmpty = !libraries.isLoading && rows.length === 0

  return (
    <>
      <PanelHeader onNew={() => setCreatorOpen(true)} />

      <section
        aria-label="Library list"
        className="mt-2 border-t border-rule-soft"
      >
        {libraries.isLoading && <LibraryRowSkeleton count={3} />}

        {isEmpty && <EmptyState onNew={() => setCreatorOpen(true)} />}

        {rows.map((lib, idx) => (
          <LibraryRow
            key={lib.id}
            library={lib}
            index={idx}
            rescanBusy={rescanMut.isPending}
            deleteBusy={deleteLibraryMut.isPending}
            onRescan={() => rescanMut.mutate(lib.id)}
            onDeleteLibrary={(purge) =>
              deleteLibraryMut.mutate({ id: lib.id, opts: { purge } })
            }
          />
        ))}
      </section>

      <LibraryCreatorDialog
        open={creatorOpen}
        onOpenChange={setCreatorOpen}
        existingNames={existingNames}
        s3Available={appConfig.data?.s3Available ?? false}
        onCreated={() => setCreatorOpen(false)}
      />
    </>
  )
}

// ---------------------------------------------------------------------------
// Header
// ---------------------------------------------------------------------------

function PanelHeader({ onNew }: { onNew: () => void }) {
  return (
    <header className="grid grid-cols-1 gap-6 pb-7 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end lg:gap-10">
      <div className="max-w-[60ch]">
        <h2 className="t-h2">Libraries</h2>
        <p className="t-small mt-3 text-(--color-ink-3) italic">
          Each library owns one filesystem root or bucket prefix, fixed at
          creation. Approved books are placed under that root by the
          library&apos;s naming pattern; on-demand scans reconcile renames and
          flag missing files.
        </p>
      </div>

      <div className="flex lg:justify-end">
        <Button
          variant="default"
          onClick={onNew}
          className="active:translate-y-[1px]"
        >
          <Icon name="plus" size={13} />
          New library
        </Button>
      </div>
    </header>
  )
}

// ---------------------------------------------------------------------------
// Row
// ---------------------------------------------------------------------------

type LibraryRowProps = {
  library: SettingsLibrary
  index: number
  rescanBusy: boolean
  deleteBusy: boolean
  onRescan: () => void
  onDeleteLibrary: (purge: boolean) => void
}

function isS3Library(lib: SettingsLibrary) {
  return lib.path === ""
}

function LibraryRow({
  library,
  index,
  rescanBusy,
  deleteBusy,
  onRescan,
  onDeleteLibrary,
}: LibraryRowProps) {
  const [confirmOpen, setConfirmOpen] = useState(false)
  const isS3 = isS3Library(library)

  const lastScanned = library.lastScannedAt
    ? formatRelative(new Date(library.lastScannedAt))
    : null

  const initial = (library.name.trim().charAt(0) || "?").toUpperCase()

  return (
    <article
      className="group relative grid grid-cols-1 gap-x-8 gap-y-5 border-b border-rule-soft py-7 transition-colors duration-300 hover:bg-(--color-paper-1) sm:grid-cols-[72px_minmax(0,1fr)_auto] sm:items-start"
      style={{
        // Stagger row mount so a fresh page load reads as a deal of cards.
        // Pure CSS — no Framer.
        animation: "lib-row-in 420ms cubic-bezier(0.16, 1, 0.3, 1) both",
        animationDelay: `${Math.min(index, 8) * 60}ms`,
      }}
    >
      {/* Left: index mark + kind label */}
      <div className="flex flex-row items-center gap-3 sm:flex-col sm:items-start sm:gap-2">
        <div
          aria-hidden
          className="grid h-[58px] w-[58px] place-items-center rounded-md bg-(--color-accent-soft) font-heading text-3xl font-medium text-(--color-accent-ink) ring-1 ring-(--color-accent-ink)/10 transition-transform duration-300 group-hover:translate-x-[2px]"
        >
          {initial}
        </div>
        <span className="text-[10px] tracking-[0.18em] text-muted-foreground uppercase">
          {isS3 ? "S3 bucket" : "Local"}
        </span>
      </div>

      {/* Center: name, slug, ribbon, path */}
      <div className="min-w-0">
        <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
          <h3 className="text-[17px] leading-tight font-medium tracking-tight">
            {library.name}
          </h3>
          <span className="font-mono text-[11.5px] text-(--color-ink-3)">
            /{library.slug}
          </span>
        </header>

        <dl className="mt-4 flex flex-wrap items-stretch divide-x divide-rule-soft">
          <Metric label="Volumes" value={library.bookCount.toLocaleString()} />
          <Metric label="Files" value={library.fileCount.toLocaleString()} />
          <Metric
            label="Last scan"
            value={lastScanned ?? "Never"}
            muted={!lastScanned}
            title={
              library.lastScannedAt
                ? new Date(library.lastScannedAt).toLocaleString()
                : undefined
            }
          />
        </dl>

        <p
          className="mt-4 flex items-center gap-2 truncate font-mono text-[12px] text-(--color-ink-3)"
          title={library.path || undefined}
        >
          <span aria-hidden className="text-(--color-ink-4)">
            ↳
          </span>
          {library.path ? (
            <span className="truncate">{library.path}</span>
          ) : (
            <span className="text-muted-foreground italic">
              Bucket prefix · libraries/{library.slug}/
            </span>
          )}
        </p>
      </div>

      {/* Right: action capsule — Rescan + overflow menu unified into one
          shell so the row carries a single, balanced control instead of a
          loose primary/destructive pair. */}
      <div className="flex flex-row items-center sm:flex-col sm:items-end">
        <RowActions
          isS3={isS3}
          rescanBusy={rescanBusy}
          deleteBusy={deleteBusy}
          onRescan={onRescan}
          onRequestDelete={() => setConfirmOpen(true)}
          libraryName={library.name}
        />
      </div>

      <DeleteLibraryDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        library={library}
        isS3={isS3}
        busy={deleteBusy}
        onConfirm={(purge) => {
          onDeleteLibrary(purge)
          setConfirmOpen(false)
        }}
      />
    </article>
  )
}

// RowActions packages Rescan + destructive overflow into a single segmented
// capsule so each row carries one balanced control instead of a loose pair
// of mismatched buttons. The overflow lives behind a kebab so the destructive
// path requires an explicit second hop (matches "destructive emphasis" rule
// for list rows).
function RowActions({
  isS3,
  rescanBusy,
  deleteBusy,
  onRescan,
  onRequestDelete,
  libraryName,
}: {
  isS3: boolean
  rescanBusy: boolean
  deleteBusy: boolean
  onRescan: () => void
  onRequestDelete: () => void
  libraryName: string
}) {
  const rescanDisabled = rescanBusy || isS3
  return (
    <div
      role="group"
      aria-label={`Actions for ${libraryName}`}
      className="inline-flex h-9 items-stretch overflow-hidden rounded-full border border-rule-soft bg-(--color-paper-0) shadow-[0_1px_0_rgba(0,0,0,0.02)]"
    >
      <button
        type="button"
        onClick={onRescan}
        disabled={rescanDisabled}
        title={
          isS3
            ? "Rescan unsupported on S3 libraries"
            : rescanBusy
              ? "Scan in progress"
              : "Reconcile drift between disk and DB"
        }
        className="inline-flex min-w-[112px] items-center justify-center gap-1.5 px-4 text-[12.5px] font-medium tracking-tight transition-colors duration-200 hover:bg-(--color-paper-1) focus-visible:ring-2 focus-visible:ring-(--color-ring) focus-visible:outline-none active:translate-y-[1px] disabled:cursor-not-allowed disabled:opacity-50"
      >
        <Icon
          name="refresh"
          size={13}
          className={rescanBusy ? "animate-spin" : undefined}
        />
        <span>{rescanBusy ? "Scanning" : "Rescan"}</span>
      </button>
      <span aria-hidden className="my-1.5 w-px bg-(--color-rule-soft)" />
      <DropdownMenu>
        <DropdownMenuTrigger
          aria-label={`More actions for ${libraryName}`}
          disabled={deleteBusy}
          className="inline-flex w-9 items-center justify-center transition-colors duration-200 hover:bg-(--color-paper-1) focus-visible:ring-2 focus-visible:ring-(--color-ring) focus-visible:outline-none active:translate-y-[1px] disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Icon name="more" size={14} />
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align="end"
          sideOffset={6}
          className="min-w-[180px]"
        >
          <DropdownMenuItem
            disabled={deleteBusy}
            onSelect={(e) => {
              e.preventDefault()
              onRequestDelete()
            }}
            className="text-destructive focus:bg-destructive/10 focus:text-destructive"
          >
            <Icon name="close" size={13} />
            <span>Delete library</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function Metric({
  label,
  value,
  muted,
  title,
}: {
  label: string
  value: string
  muted?: boolean
  title?: string
}) {
  return (
    <div
      className="flex min-w-[88px] flex-col px-4 first:pl-0 last:pr-0"
      title={title}
    >
      <span
        className={
          "font-heading text-[18px] leading-none tracking-tight tabular-nums " +
          (muted ? "text-(--color-ink-4) italic" : "")
        }
      >
        {value}
      </span>
      <span className="mt-1.5 text-[10px] tracking-[0.16em] text-muted-foreground uppercase">
        {label}
      </span>
    </div>
  )
}

// ---------------------------------------------------------------------------
// States
// ---------------------------------------------------------------------------

function LibraryRowSkeleton({ count }: { count: number }) {
  return (
    <>
      {Array.from({ length: count }).map((_, i) => (
        <div
          key={i}
          className="grid grid-cols-[72px_minmax(0,1fr)_auto] items-start gap-x-8 border-b border-rule-soft py-7"
          style={{
            animation: "lib-row-in 420ms cubic-bezier(0.16, 1, 0.3, 1) both",
            animationDelay: `${i * 80}ms`,
          }}
        >
          <div className="h-[58px] w-[58px] animate-pulse rounded-md bg-(--color-paper-2)" />
          <div className="flex flex-col gap-3">
            <div className="h-4 w-44 animate-pulse rounded-sm bg-(--color-paper-2)" />
            <div className="h-3 w-72 animate-pulse rounded-sm bg-(--color-paper-2)" />
            <div className="h-3 w-96 animate-pulse rounded-sm bg-(--color-paper-2)" />
          </div>
          <div className="h-8 w-24 animate-pulse rounded-md bg-(--color-paper-2)" />
        </div>
      ))}
    </>
  )
}

function EmptyState({ onNew }: { onNew: () => void }) {
  return (
    <NotebookEmpty
      title="An empty stack."
      body="A library is the home for one filesystem root or one bucket prefix. Create the first one and embookshelf will start placing approved books there."
      action={
        <Button onClick={onNew} className="active:translate-y-[1px]">
          <Icon name="plus" size={13} />
          Create library
        </Button>
      }
    />
  )
}

// ---------------------------------------------------------------------------
// Creator dialog
// ---------------------------------------------------------------------------

type LibraryCreatorDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  existingNames: Array<string>
  s3Available: boolean
  onCreated: () => void
}

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

  useEffect(() => {
    if (open) return
    // Deliberate: setState inside an effect, syncing React state from an
    // external source. Was suppressed via react-hooks/set-state-in-effect;
    // Biome has no equivalent rule yet, so there is nothing to suppress.
    setName("")
    setKind("local")
    setScanOnCreate(true)
  }, [open])

  const trimmedName = name.trim()
  const nameCollision = existingNames.some(
    (existing) => existing.toLowerCase() === trimmedName.toLowerCase()
  )
  const nameValid = trimmedName !== "" && !nameCollision

  const slug = trimmedName !== "" ? slugify(trimmedName) : ""
  const derivedPrefix =
    kind === "s3" && slug !== "" ? `libraries/${slug}/` : null

  const createMut = useApiMutation(createLibrary, {
    successToast: "Library created.",
    onSuccess: () => onCreated(),
  })

  const submitDisabled = !nameValid || createMut.isPending

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[540px]">
        <DialogHeader>
          <DialogTitle>New library</DialogTitle>
          <DialogDescription>
            Pick a backing store. The choice is fixed at creation; the path is
            derived from the name.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-5">
          <div className="flex flex-col gap-2">
            <Label htmlFor="lib-name">Name</Label>
            <Input
              id="lib-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Fiction"
              autoFocus
            />
            {trimmedName !== "" && nameCollision && (
              <p className="t-small text-(--color-accent-ink)">
                A library with that name already exists.
              </p>
            )}
            {trimmedName !== "" && !nameCollision && (
              <p className="font-mono text-[11px] text-muted-foreground">
                slug · {slug}
              </p>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label>Storage</Label>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <KindCard
                active={kind === "local"}
                onClick={() => setKind("local")}
                title="Local filesystem"
                hint={`Created at (data path)/libraries/${slug || "<slug>"}/`}
                meta="On-disk root, fixed after creation"
              />
              <KindCard
                active={kind === "s3"}
                disabled={!s3Available}
                onClick={() => s3Available && setKind("s3")}
                title="S3 bucket"
                hint={
                  s3Available
                    ? (derivedPrefix ?? "Enter a name to preview prefix")
                    : "Set EMBOOKSHELF_S3_BUCKET to enable"
                }
                meta="Shared bucket, per-library prefix"
              />
            </div>
          </div>

          <label className="flex cursor-pointer items-center gap-3 text-sm text-(--color-ink-2)">
            <Switch
              checked={scanOnCreate}
              onCheckedChange={(v) => setScanOnCreate(Boolean(v))}
            />
            <span>Run an initial scan after creating</span>
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
            onClick={() =>
              createMut.mutate({
                name: trimmedName,
                kind,
                scan: scanOnCreate,
              })
            }
            disabled={submitDisabled}
            className="active:translate-y-[1px]"
          >
            {createMut.isPending ? "Creating…" : "Create library"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function KindCard({
  active,
  disabled,
  onClick,
  title,
  hint,
  meta,
}: {
  active: boolean
  disabled?: boolean
  onClick: () => void
  title: string
  hint: string
  meta: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-pressed={active}
      className={
        "group relative flex flex-col gap-2 rounded-md border p-4 text-left transition-all duration-200 " +
        (active
          ? "border-(--color-accent-ink) bg-(--color-accent-soft)/40 ring-1 ring-(--color-accent-ink)/15 "
          : "border-rule-soft bg-(--color-paper-0) hover:border-(--color-rule) ") +
        (disabled ? "cursor-not-allowed opacity-50 " : "cursor-pointer ") +
        "active:translate-y-[1px]"
      }
    >
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium tracking-tight">{title}</span>
        {active && (
          <span
            aria-hidden
            className="inline-flex h-4 w-4 items-center justify-center rounded-full bg-(--color-accent-ink) text-(--color-paper-0)"
          >
            <Icon name="check" size={10} />
          </span>
        )}
      </div>
      <span className="font-mono text-[11px] break-all text-(--color-ink-3)">
        {hint}
      </span>
      <span className="text-[10px] tracking-[0.14em] text-muted-foreground uppercase">
        {meta}
      </span>
    </button>
  )
}

// ---------------------------------------------------------------------------
// Delete dialog
// ---------------------------------------------------------------------------

// The S3 purge toggle is the extra-switch slot's existing case: it rides
// along with the confirmation, so the dialog owns it and hands its value
// to onConfirm rather than the panel keeping a second piece of state it
// would have to remember to reset.
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
  return (
    <ConfirmPhraseDialog<boolean>
      open={open}
      onOpenChange={onOpenChange}
      title="Delete library"
      description={
        <>
          Removes <strong>{library.name}</strong>, all {library.bookCount} book
          records, their cover images, and every annotation, reading session,
          and shelf assignment inside it.{" "}
          {isS3
            ? "S3 objects are left alone unless you check the purge option below."
            : "Source files on disk are left alone."}
        </>
      }
      phrase={library.name}
      confirmLabel="Delete library"
      busyLabel="Deleting…"
      busy={busy}
      extras={{
        initial: false,
        render: (purge, setPurge) =>
          isS3 ? (
            <label className="flex cursor-pointer items-center gap-3 text-sm">
              <Switch
                checked={purge}
                onCheckedChange={(v) => setPurge(Boolean(v))}
              />
              <span>Also delete every object in the S3 bucket prefix</span>
            </label>
          ) : null,
      }}
      onConfirm={onConfirm}
    />
  )
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatRelative renders a short human-friendly delta ("3h ago", "yesterday",
// "Mar 14") so the row stays scannable instead of hosting a full timestamp.
// Falls back to a locale date for anything older than a week.
function formatRelative(d: Date): string {
  const now = Date.now()
  const diffMs = now - d.getTime()
  const sec = Math.round(diffMs / 1000)
  if (sec < 60) return "just now"
  const min = Math.round(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.round(min / 60)
  if (hr < 24) return `${hr}h ago`
  const day = Math.round(hr / 24)
  if (day === 1) return "yesterday"
  if (day < 7) return `${day}d ago`
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}
