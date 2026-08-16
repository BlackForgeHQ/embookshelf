import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"

import type { ApiError, ApiErrorCode } from "@/api/client"
import type { BookDetail as BookDetailPayload } from "@/api/books"
import {
  annotationKind,
  bookAnnotationsQuery,
  createAnnotation,
  deleteAnnotation,
} from "@/api/annotations"
import { meQuery } from "@/api/auth"
import { useApiMutation } from "@/api/mutation"
import {
  bookQuery,
  bookQueryKey,
  deleteBook,
  patchBook,
  sendBookToKindle,
  shelvesQueryKey,
} from "@/api/books"
import { appConfigQuery } from "@/api/settings"
import type { Viewer } from "@/lib/affordance"
import { affordanceFor, messageForCode, viewerOf } from "@/lib/affordance"
import { formatDate } from "@/lib/format"
import { isKindleEligibleFormat } from "@/lib/formats"
import {
  DEVICE_KIND_LABELS,
  devicesQuery,
  sendBookToDevice,
} from "@/api/devices"
import { useApiQuery } from "@/api/query"
import { locatorLabel } from "@/lib/locator"
import { ConfirmPhraseDialog } from "@/components/ConfirmPhraseDialog"
import { Cover, StarRating } from "@/components/Cover"
import { DefRow } from "@/components/DefRow"
import { Icon } from "@/components/Icon"
import { Notice } from "@/components/Notice"
import { ProgressBar } from "@/components/ProgressBar"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { AudiobookPanel } from "@/components/book/AudiobookPanel"
import { ReadingGuidePanel } from "@/components/book/ReadingGuidePanel"
import { ShelfMembership } from "@/components/book/ShelfMembership"
import { VersionRows } from "@/components/book/VersionRows"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

// TABS is the single source the union derives from, so the type can
// never lie about what renders again — it declared five members while
// seven tabs rendered, hidden by an `as Tab` cast (#352).
const TABS = [
  "overview",
  "notes",
  "annotations",
  "guide",
  "narration",
  "versions",
  "activity",
] as const
type Tab = (typeof TABS)[number]
const isTab = (v: string): v is Tab => (TABS as readonly string[]).includes(v)

export const Route = createFileRoute("/_app/book/$id")({
  component: BookDetail,
})

function BookDetail() {
  const { id } = Route.useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<Tab>("overview")
  const [deleteOpen, setDeleteOpen] = useState(false)

  const book = useApiQuery(bookQuery(id))
  const me = useApiQuery(meQuery)
  const viewer = viewerOf(me.data)

  // Inline: a refused delete is read next to the button that refused,
  // inside the danger zone the reader deliberately opened.
  const deleteMut = useApiMutation(deleteBook, {
    reportErrors: "inline",
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: bookQueryKey(id) })
      queryClient.invalidateQueries({ queryKey: shelvesQueryKey })
      void navigate({ to: "/library" })
    },
  })
  const deleteError = deleteMut.error

  const ratingMut = useApiMutation(patchBook, {
    errorToast: (err) => err.message || "Rating failed.",
    onSuccess: (updated) => {
      queryClient.setQueryData(bookQueryKey(id), updated)
    },
  })

  if (book.isLoading) {
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small">Loading…</p>
      </div>
    )
  }
  if (book.isError) {
    const err = book.error as unknown as ApiError
    return (
      <div style={{ padding: 40 }}>
        <p className="t-small" style={{ color: "var(--color-accent-ink)" }}>
          {err.status === 404 ? "Book not found." : "Failed to load book."}
        </p>
      </div>
    )
  }
  if (!book.data) return null

  const b = book.data
  const progress = b.progress

  return (
    <div className="fade-in">
      <div
        style={{
          padding: "16px 32px",
          borderBottom: "1px solid var(--color-rule-soft)",
          display: "flex",
          alignItems: "center",
          gap: 12,
        }}
      >
        <Button
          variant="ghost"
          size="sm"
          onClick={() => void navigate({ to: "/library" })}
        >
          <Icon name="arrow-left" size={14} /> Back to library
        </Button>
        <div className="grow" />
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            void navigate({ to: "/book/$id/edit", params: { id } })
          }
        >
          <Icon name="edit" size={13} /> Edit metadata
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={() =>
            void navigate({ to: "/book/$id/find", params: { id } })
          }
        >
          <Icon name="search" size={13} /> Find metadata online
        </Button>
        <Button variant="outline" size="sm" asChild>
          <a
            href={`/api/v1/books/${id}/file?download=1`}
            // `download` hints the browser save-as; the server already
            // sets Content-Disposition: attachment with the right
            // filename, so this attribute is mostly belt-and-braces.
            download
          >
            <Icon name="download" size={13} /> Download
          </a>
        </Button>
        <SendToKindleButton
          book={b}
          kindleEmail={me.data?.kindleEmail ?? ""}
          viewer={viewer}
        />
        <SendToDeviceButton bookId={id} />
      </div>

      <div
        className="page-split page-split--cover-main"
        style={{ padding: "40px 48px" }}
      >
        {/* Left — cover & actions. Capped and centered so the stacked
            mobile layout doesn't stretch the rail (and the cover with
            it) across the whole viewport; on desktop the 280px grid
            column is narrower than the cap, so nothing changes. */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 20,
            width: "100%",
            maxWidth: 320,
            marginInline: "auto",
          }}
        >
          <Cover book={b} size="hero" />
          <Button
            size="lg"
            className="w-full"
            onClick={() => void navigate({ to: "/read/$id", params: { id } })}
          >
            <Icon name="book-open" size={14} />{" "}
            {progress > 0 && progress < 1 ? "Continue reading" : "Open book"}
          </Button>
          {progress > 0 && progress < 1 && (
            <div>
              <div
                style={{
                  display: "flex",
                  justifyContent: "space-between",
                  marginBottom: 6,
                }}
              >
                <span className="t-micro">Progress</span>
                <span className="mono" style={{ fontSize: 11 }}>
                  {Math.round(progress * 100)}%
                </span>
              </div>
              <ProgressBar value={progress} label="Reading progress" />
            </div>
          )}
          <ShelfMembership book={b} />
        </div>

        {/* Right — info */}
        <div>
          <div className="t-micro" style={{ marginBottom: 8 }}>
            {b.format}
            {b.year ? ` · ${b.year}` : ""}
          </div>
          <h1
            className="t-display"
            style={{ marginBottom: 6, textWrap: "balance" }}
          >
            {b.title}
          </h1>
          <div
            style={{
              fontSize: 17,
              color: "var(--color-ink-2)",
              fontStyle: "italic",
              marginBottom: 16,
            }}
          >
            by {b.author}
          </div>

          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 16,
              marginBottom: 28,
            }}
          >
            <StarRating
              rating={b.rating}
              size={18}
              onChange={(next) =>
                ratingMut.mutate({ id, patch: { rating: next } })
              }
              disabled={ratingMut.isPending}
            />
            <span
              className="mono"
              style={{ fontSize: 12, color: "var(--color-ink-2)" }}
            >
              {b.rating.toFixed(1)}
            </span>
            {b.tags.length > 0 && (
              <span style={{ color: "var(--color-rule)" }}>·</span>
            )}
            {b.tags.map((t) => (
              <span key={t} className="chip">
                {t}
              </span>
            ))}
          </div>

          {b.description && (
            <p
              style={{
                fontSize: 16,
                lineHeight: 1.65,
                color: "var(--color-ink-1)",
                marginBottom: 32,
                maxWidth: 640,
                textWrap: "pretty",
              }}
            >
              {b.description}
            </p>
          )}

          <Tabs
            value={tab}
            onValueChange={(v) => isTab(v) && setTab(v)}
            className="mb-6"
          >
            <TabsList
              variant="line"
              className="h-9 w-full justify-start gap-4 border-b border-(--color-rule-soft) px-0"
            >
              <TabsTrigger value="overview" className="flex-none px-3">
                Overview
              </TabsTrigger>
              <TabsTrigger value="guide" className="flex-none px-3">
                Guide
              </TabsTrigger>
              <TabsTrigger value="narration" className="flex-none px-3">
                Narration
              </TabsTrigger>
              <TabsTrigger value="notes" className="flex-none px-3">
                Notes
              </TabsTrigger>
              <TabsTrigger value="annotations" className="flex-none px-3">
                Annotations
              </TabsTrigger>
              <TabsTrigger value="versions" className="flex-none px-3">
                Versions
              </TabsTrigger>
              <TabsTrigger value="activity" className="flex-none px-3">
                Activity
              </TabsTrigger>
            </TabsList>

            <TabsContent value="overview">
              <div style={{ maxWidth: 640 }}>
                <DefRow label="Title" value={b.title} {...META_ROW} />
                <DefRow label="Author" value={b.author} {...META_ROW} />
                {b.series && (
                  <DefRow
                    label="Series"
                    value={`${b.series}${
                      b.seriesNum ? `, Book ${b.seriesNum}` : ""
                    }`}
                    {...META_ROW}
                  />
                )}
                {b.year ? (
                  <DefRow label="Published" value={b.year} {...META_ROW} />
                ) : null}
                <DefRow label="Format" value={b.format} {...META_ROW} />
                {b.publisher && (
                  <DefRow label="Publisher" value={b.publisher} {...META_ROW} />
                )}
                {b.tags.length > 0 && (
                  <DefRow
                    label="Categories"
                    value={b.tags.join(" · ")}
                    {...META_ROW}
                  />
                )}
                <DefRow
                  label="Added"
                  value={formatDate(b.addedAt)}
                  {...META_ROW}
                />
                {b.isbn && (
                  <DefRow
                    label="ISBN"
                    value={
                      <span className="mono" style={{ fontSize: 11.5 }}>
                        {b.isbn}
                      </span>
                    }
                    {...META_ROW}
                  />
                )}
              </div>
            </TabsContent>

            <TabsContent value="guide">
              <ReadingGuidePanel bookId={id} format={b.format} />
            </TabsContent>

            <TabsContent value="narration">
              <AudiobookPanel bookId={id} format={b.format} />
            </TabsContent>

            <TabsContent value="notes">
              <NotesPanel bookId={id} />
            </TabsContent>

            <TabsContent value="annotations">
              <div className="t-small" style={{ fontStyle: "italic" }}>
                No PDF annotations for this book.
              </div>
            </TabsContent>

            <TabsContent value="versions">
              <div
                style={{
                  maxWidth: 640,
                  display: "flex",
                  flexDirection: "column",
                  gap: 24,
                }}
              >
                <VersionRows
                  bookId={id}
                  title={b.title}
                  format={b.format}
                  isAdmin={viewer.isAdmin}
                />

                {viewer.isAdmin && (
                  <div
                    style={{
                      padding: 16,
                      border: "1px solid var(--color-destructive)",
                      background: "var(--color-paper-0)",
                    }}
                  >
                    <div
                      className="t-label"
                      style={{
                        marginBottom: 6,
                        color: "var(--color-destructive)",
                      }}
                    >
                      Danger zone
                    </div>
                    <p
                      className="t-small"
                      style={{ marginBottom: 10, maxWidth: 520 }}
                    >
                      Permanently remove this book, its cover, its source file,
                      and every reader&apos;s progress, notes, and shelf
                      placements for it. This cannot be undone.
                    </p>
                    {deleteError && (
                      <Notice className="mb-2.5">{deleteError.message}</Notice>
                    )}
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      disabled={deleteMut.isPending}
                      onClick={() => setDeleteOpen(true)}
                    >
                      <Icon name="close" size={12} />{" "}
                      {deleteMut.isPending ? "Deleting…" : "Delete book"}
                    </Button>

                    <DeleteBookDialog
                      open={deleteOpen}
                      onOpenChange={setDeleteOpen}
                      title={b.title}
                      busy={deleteMut.isPending}
                      onConfirm={() => {
                        deleteMut.mutate(id)
                        setDeleteOpen(false)
                      }}
                    />
                  </div>
                )}
              </div>
            </TabsContent>

            <TabsContent value="activity">
              <div className="t-small" style={{ fontStyle: "italic" }}>
                Per-book activity timeline lands once reading sessions are
                tracked server-side.
              </div>
            </TabsContent>
          </Tabs>
        </div>
      </div>
    </div>
  )
}

// The overview's bibliographic rows: a narrower label column than the
// settings Cards use, and ruled, because the block reads as a ledger.
const META_ROW = { labelWidth: 96, rule: true } as const

// NotesPanel renders every annotation on this book + an inline "new
// note" composer. The composer always creates a margin note
// (`selectedText` stays empty) — highlights come from the EPUB reader's
// selection flow, not from typing here.
function NotesPanel({ bookId }: { bookId: string }) {
  const annotations = useApiQuery(bookAnnotationsQuery(bookId))

  // Inline: the panel renders `error` under the compose box, so a note
  // that would not save says so where the note still is.
  const createMut = useApiMutation(createAnnotation, {
    reportErrors: "inline",
  })
  const deleteMut = useApiMutation(deleteAnnotation, {
    reportErrors: "inline",
  })

  const [draft, setDraft] = useState("")
  const rows = annotations.data ?? []
  const error = createMut.error ?? deleteMut.error

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        gap: 14,
        maxWidth: 640,
      }}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault()
          const value = draft.trim()
          if (!value) return
          createMut.mutate({ bookId, body: { note: value } })
          setDraft("")
        }}
        style={{
          display: "flex",
          flexDirection: "column",
          gap: 8,
          background: "var(--color-paper-0)",
          border: "1px solid var(--color-rule-soft)",
          padding: 12,
          borderRadius: 2,
        }}
      >
        <Textarea
          rows={3}
          placeholder="Add a note about this book…"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="resize-y"
        />
        <div style={{ display: "flex", justifyContent: "flex-end" }}>
          <Button
            type="submit"
            size="sm"
            disabled={createMut.isPending || draft.trim() === ""}
          >
            <Icon name="plus" size={12} />{" "}
            {createMut.isPending ? "Saving…" : "Add note"}
          </Button>
        </div>
      </form>

      {error && <Notice>{error.message}</Notice>}

      {annotations.isLoading && (
        <div className="t-small" style={{ fontStyle: "italic" }}>
          Loading notes…
        </div>
      )}
      {!annotations.isLoading && rows.length === 0 && (
        <div className="t-small" style={{ fontStyle: "italic" }}>
          No notes yet. Highlights and margin notes you take while reading will
          appear here.
        </div>
      )}
      {rows.map((a) => {
        const kind = annotationKind(a)
        return (
          <div
            key={a.id}
            style={{
              borderLeft: "3px solid var(--color-accent-soft)",
              padding: "8px 14px",
              background: "var(--color-paper-0)",
            }}
          >
            <div
              style={{
                display: "flex",
                justifyContent: "space-between",
                marginBottom: 4,
              }}
            >
              <span className="t-micro">
                {kind === "highlight"
                  ? "Highlight"
                  : kind === "highlight+note"
                    ? "Highlight · Note"
                    : "Note"}
                {a.locator && ` · ${locatorLabel(a.locator)}`}
              </span>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                <span className="t-micro">{formatDate(a.createdAt)}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => deleteMut.mutate({ id: a.id, bookId })}
                  disabled={deleteMut.isPending}
                  aria-label="Delete"
                  title="Delete"
                >
                  <Icon name="close" size={11} />
                </Button>
              </div>
            </div>
            {a.selectedText && (
              <p
                style={{
                  fontSize: 14.5,
                  lineHeight: 1.55,
                  fontStyle: "italic",
                  background: "var(--color-highlight)",
                  padding: "4px 8px",
                  marginBottom: a.note ? 8 : 0,
                }}
              >
                {a.selectedText}
              </p>
            )}
            {a.note && (
              <p style={{ fontSize: 14.5, lineHeight: 1.55 }}>{a.note}</p>
            )}
          </div>
        )
      })}
    </div>
  )
}

/**
 * The code `POST /books/:id/kindle` would answer with, or null if it
 * would accept.
 *
 * The branch order is Handler.SendToKindle's, and deliberately so: the
 * handler checks the transport, then the caller's address, then the
 * book's format (internal/handler/kindle.go). Reading them in any other
 * order predicts a refusal the server would not have sent — which is
 * what this did before #171, telling a user with no Kindle address about
 * a format rule the server would never have reached.
 *
 * The format is the book's **primary** one, deliberately: Send-to-Kindle
 * ships the file the library ingested, not whichever Rendition the
 * reader last opened. Those diverge for a narrated book (ADR-0025 §3).
 * If it ever offers the narration, this becomes a Rendition question and
 * moves to `renditionsFor`.
 */
export function kindleRefusal(
  emailEnabled: boolean,
  kindleEmail: string,
  format: string,
): ApiErrorCode | null {
  if (!emailEnabled) return "EMAIL_DISABLED"
  if (kindleEmail.trim() === "") return "KINDLE_EMAIL_UNSET"
  if (!isKindleEligibleFormat(format)) return "FORMAT_NOT_SUPPORTED"
  return null
}

// SendToKindleButton fires the book at the user's @kindle.com address.
// What it does when it can't — hide, explain, or lead to the fix — is not
// this component's call: the rule above names the refusal the server
// would send and lib/affordance.ts decides the affordance and writes the
// sentence (#171). All that is left here is the router and the markup.
function SendToKindleButton({
  book,
  kindleEmail,
  viewer,
}: {
  book: BookDetailPayload
  kindleEmail: string
  viewer: Viewer
}) {
  const navigate = useNavigate()
  const cfg = useApiQuery(appConfigQuery)

  const sendMut = useApiMutation(sendBookToKindle, {
    successToast: "Send-to-Kindle queued.",
    errorToast: (e) =>
      messageForCode(e.code, e.message, viewer) || "Send to Kindle failed.",
  })

  const refusal = kindleRefusal(
    // Tri-state on purpose: `undefined` means the config has not landed
    // yet, which is not grounds to claim email is off.
    cfg.data?.emailEnabled !== false,
    kindleEmail,
    book.format,
  )
  const action = refusal === null
    ? ({ kind: "send" } as const)
    : affordanceFor(refusal, viewer)

  if (action.kind === "hidden") {
    return null
  }

  if (action.kind === "navigate") {
    // The Fix says where, not how — affordance.ts holds no router. A
    // settings fix can only land on /settings because the active panel
    // is that route's local state, which is why the sentence names it.
    const fix = action.fix
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() =>
          void navigate(
            fix.where === "account"
              ? { to: "/account", search: { section: "account" } }
              : { to: "/settings" }
          )
        }
        title={action.reason}
      >
        <Icon name="device" size={13} /> {action.label}
      </Button>
    )
  }

  if (action.kind !== "send") {
    // "explain", plus "report" for a code this build has never heard of:
    // visible, refused, and saying why.
    return (
      <Button variant="outline" size="sm" disabled title={action.reason}>
        <Icon name="device" size={13} /> Send to Kindle
      </Button>
    )
  }

  return (
    <Button
      variant="outline"
      size="sm"
      disabled={sendMut.isPending}
      onClick={() => sendMut.mutate(book.id)}
      title={`Send to ${kindleEmail}`}
    >
      <Icon name="device" size={13} />{" "}
      {sendMut.isPending ? "Sending…" : "Send to Kindle"}
    </Button>
  )
}

// SendToDeviceButton opens a tiny dropdown of paired devices. If none are
// paired, the button navigates to the account page on the Devices section.
function SendToDeviceButton({ bookId }: { bookId: string }) {
  const navigate = useNavigate()
  const devices = useApiQuery(devicesQuery)
  const [open, setOpen] = useState(false)

  const sendMut = useApiMutation(sendBookToDevice, {
    successToast: (_data, vars) => {
      const target = devices.data?.find((d) => d.id === vars.deviceId)
      return `Sent to ${target?.name ?? "device"}.`
    },
    errorToast: (e) => e.message || "Send failed.",
    onSuccess: () => setOpen(false),
  })

  const list = devices.data ?? []

  if (list.length === 0) {
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() =>
          navigate({ to: "/account", search: { section: "devices" } })
        }
        title="No devices paired. Pair one in Account → Device sync"
      >
        <Icon name="device" size={13} /> Send to device
      </Button>
    )
  }

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" disabled={sendMut.isPending}>
          <Icon name="device" size={13} />{" "}
          {sendMut.isPending ? "Sending…" : "Send to device"}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        {list.map((d) => (
          <DropdownMenuItem
            key={d.id}
            onSelect={() => sendMut.mutate({ bookId, deviceId: d.id })}
            className="flex flex-col items-start gap-0.5"
          >
            <span className="t-item-title">{d.name}</span>
            <span className="t-small" style={{ fontSize: 11 }}>
              {DEVICE_KIND_LABELS[d.kind]}
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// DeleteBookDialog confirms a destructive book teardown. The "type the
// title to confirm" gate matches the weight of the operation — the DB
// row, its cover, the source file on disk, and every reader's notes,
// progress, and shelf placements go with it. The gate itself is
// ConfirmPhraseDialog's; this is only the consequence copy.
function DeleteBookDialog({
  open,
  onOpenChange,
  title,
  busy,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  busy: boolean
  onConfirm: () => void
}) {
  return (
    <ConfirmPhraseDialog
      open={open}
      onOpenChange={onOpenChange}
      title="Delete book"
      description={
        <>
          Permanently remove <strong>{title}</strong>: the DB row, its cover,
          its source file on disk, and every reader&apos;s progress, notes, and
          shelf placements for it. This cannot be undone.
        </>
      }
      phrase={title}
      // A title is long enough that echoing it in the instruction reads
      // worse than pointing at it; the placeholder carries the text.
      prompt="Type the title to confirm."
      placeholder={title}
      confirmLabel="Delete book"
      busyLabel="Deleting…"
      busy={busy}
      onConfirm={onConfirm}
    />
  )
}
