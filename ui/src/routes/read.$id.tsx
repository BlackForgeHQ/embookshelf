import { useMemo, useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"

import type { BookDetail } from "@/api/books"
import type { AudioProgress, AudioReaderHandle } from "@/components/AudioReader"
import type {
  ComicFitMode,
  ComicProgress,
  ComicReaderHandle,
} from "@/components/ComicReader"
import type {
  EpubHighlight,
  EpubProgress,
  EpubReaderHandle,
  EpubTocEntry,
} from "@/components/EpubReader"
import type { PdfProgress, PdfReaderHandle } from "@/components/PdfReader"
import type { Locator } from "@/lib/locator"
import { useApiMutation } from "@/api/mutation"
import { useReadingPosition } from "@/hooks/useReadingPosition"
import { isNarratableFormat, readerKindForFormat } from "@/lib/formats"
import { ProgressBar } from "@/components/ProgressBar"
import {
  decodeLocator,
  encodeLocator,
  formatHMS,
  locatorLabel,
} from "@/lib/locator"
import {
  annotationKind,
  bookAnnotationsQuery,
  createAnnotation,
  createBookmark,
  deleteAnnotation,
} from "@/api/annotations"
import { bookAudiobookQuery, narrationUrl } from "@/api/audiobooks"
import { bookQuery, updateProgress } from "@/api/books"
import { useApiQuery } from "@/api/query"
import { AudioReader } from "@/components/AudioReader"
import { ComicReader } from "@/components/ComicReader"
import { EpubReader } from "@/components/EpubReader"
import { Icon } from "@/components/Icon"
import { PdfReader } from "@/components/PdfReader"
import { Button } from "@/components/ui/button"

export const Route = createFileRoute("/read/$id")({
  component: Reader,
})

/**
 * How long a reading position is held before it is written. Page turns
 * land every few seconds, so a short window keeps the server roughly in
 * step without a request per turn.
 */
const PROGRESS_DEBOUNCE_MS = 600
/**
 * Audio timeupdate fires several times a second, so it gets a much longer
 * window. The cost of the longer wait is paid back by flushing on pause and
 * on exit, which is when a listening session actually ends.
 */
const AUDIO_PROGRESS_DEBOUNCE_MS = 5000

type TocItem = { label: string; href: string }

function Reader() {
  const { id } = Route.useParams()
  const navigate = useNavigate()

  const book = useApiQuery(bookQuery(id))

  if (book.isLoading) {
    return <FullScreenMessage>Loading…</FullScreenMessage>
  }
  if (book.isError || !book.data) {
    return <FullScreenMessage>Book not found.</FullScreenMessage>
  }
  const b = book.data
  // Which surface opens the book comes from the shared format table, so
  // the route cannot send a book somewhere the server will not serve
  // bytes for. The Rendition choice below is a separate decision (#194).
  const reader = readerKindForFormat(b.format)
  if (reader === "comic") {
    return <ComicReaderShell book={b} />
  }
  if (reader === "audio") {
    return <AudioReaderShell book={b} />
  }
  if (reader === null) {
    return (
      <FullScreenMessage>
        Reader not implemented for <code>{b.format}</code> yet.
        <div style={{ marginTop: 16 }}>
          <Button
            variant="outline"
            onClick={() => void navigate({ to: "/book/$id", params: { id } })}
          >
            <Icon name="arrow-left" size={14} /> Back
          </Button>
        </div>
      </FullScreenMessage>
    )
  }

  // Narratable rather than EPUB: what makes a book worth checking for an
  // audio rendition is that something could have narrated it, which is
  // the same question the panel and the handler ask (#192).
  if (isNarratableFormat(b.format)) {
    return <NarratableShell book={b} />
  }
  return <ReaderShell book={b} />
}

// NarratableShell picks which rendition of an EPUB to open.
//
// books.format names the *primary* format, so an EPUB with a narration
// would otherwise always open the text reader and the audio would be
// unreachable from here — the concrete cost of ADR-0025 §3 moving the
// dispatch key off that column.
//
// The switch lives here rather than inside either shell: both are large,
// self-contained, and have no business knowing the other exists.
function NarratableShell({ book }: { book: BookDetail }) {
  const narration = useApiQuery(bookAudiobookQuery(book.id))
  const [listening, setListening] = useState(false)

  const ready = narration.data?.state === "ready"
  // Falling back to text when the narration is not ready matters on a
  // reload mid-run: the toggle vanishes rather than opening a player with
  // nothing behind it.
  const listen = listening && ready

  return (
    <>
      {ready && <RenditionSwitch listening={listen} onChange={setListening} />}
      {listen ? (
        <AudioReaderShell book={book} audioUrl={narrationUrl(book.id)} />
      ) : (
        <ReaderShell book={book} />
      )}
    </>
  )
}

// RenditionSwitch floats above whichever shell is mounted, because
// neither one owns the choice.
function RenditionSwitch({
  listening,
  onChange,
}: {
  listening: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div
      style={{
        position: "fixed",
        bottom: 16,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 60,
        display: "flex",
        gap: 2,
        padding: 2,
        borderRadius: 999,
        background: "var(--color-surface, #fff)",
        border: "1px solid var(--color-rule-soft)",
        boxShadow: "0 2px 10px rgb(0 0 0 / 0.12)",
      }}
    >
      <Button
        size="sm"
        variant={listening ? "ghost" : "outline"}
        style={{ borderRadius: 999 }}
        onClick={() => onChange(false)}
      >
        Read
      </Button>
      <Button
        size="sm"
        variant={listening ? "outline" : "ghost"}
        style={{ borderRadius: 999 }}
        onClick={() => onChange(true)}
      >
        <Icon name="play" size={14} /> Listen
      </Button>
    </div>
  )
}

function ReaderShell({ book }: { book: BookDetail }) {
  const navigate = useNavigate()
  // This shell drives EPUB and PDF, so it takes the kind that matches the
  // format and ignores the other — a token of the wrong kind means "start
  // from the beginning" rather than a coerced position.
  const resume = decodeLocator(book.resumeCfi)
  const resumeCfi = resume?.kind === "cfi" ? resume.cfi : undefined
  const resumePage = resume?.kind === "page" ? resume.page : undefined

  const [chromeVisible, setChromeVisible] = useState(true)
  const [tocOpen, setTocOpen] = useState(false)
  const [notesOpen, setNotesOpen] = useState(false)
  const [typePanelOpen, setTypePanelOpen] = useState(false)
  const [toc, setToc] = useState<Array<TocItem>>([])

  // Progress state mirrors what the reader reports. Used for the bottom
  // scrubber and to compose the token we persist on unmount.
  const [percent, setPercent] = useState(0)
  const [pageState, setPageState] = useState<{
    current: number
    total: number
  } | null>(null)
  const [cfiState, setCfiState] = useState<string>(resumeCfi ?? "")

  // Pending EPUB selection — set by rendition.on('selected'), cleared
  // when the user saves or dismisses it. Absence hides the selection
  // toolbar, so this doubles as the toolbar's visibility switch.
  const [pendingSelection, setPendingSelection] = useState<{
    cfiRange: string
    text: string
  } | null>(null)

  const epubRef = useRef<EpubReaderHandle>(null)
  const pdfRef = useRef<PdfReaderHandle>(null)

  const { queue: queueProgress, flush: flushProgress } = useReadingPosition({
    save: (progress, token) => void updateProgress(book.id, progress, token),
    debounceMs: PROGRESS_DEBOUNCE_MS,
  })

  // Annotations for this book — drives the side panel AND the EPUB
  // highlight overlay.
  const annotations = useApiQuery(bookAnnotationsQuery(book.id))

  const createAnnotationMut = useApiMutation(createAnnotation, {
    onSuccess: () => setPendingSelection(null),
  })

  const deleteAnnotationMut = useApiMutation(deleteAnnotation)

  const bookmarkMut = useApiMutation(createBookmark, {
    successToast: "Bookmark saved",
    errorToast: (err) => err.message || "Bookmark failed",
  })

  const onBookmark = () => {
    const locator: Locator | null =
      book.format === "PDF" && pageState
        ? { kind: "page", page: pageState.current }
        : book.format === "EPUB" && cfiState
          ? { kind: "cfi", cfi: cfiState }
          : null
    if (!locator) {
      toast.info("Open the book first, then bookmark.")
      return
    }
    bookmarkMut.mutate({ bookId: book.id, locator })
  }

  // EPUB highlights for the rendition overlay. Stable reference when the
  // annotation list hasn't changed, so the effect in EpubReader doesn't
  // churn add/remove on every render.
  const epubHighlights = useMemo<Array<EpubHighlight>>(() => {
    if (book.format !== "EPUB") return []
    return (annotations.data ?? [])
      .filter(
        (a) => !!a.selectedText && decodeLocator(a.locator)?.kind === "cfi"
      )
      .map((a) => ({ cfiRange: a.locator!, color: "oklch(0.92 0.07 85)" }))
  }, [book.format, annotations.data])

  const onEpubProgress = (p: EpubProgress) => {
    setPercent(p.percent)
    setCfiState(p.cfi)
    queueProgress(p.percent, p.cfi)
  }
  const onPdfProgress = (p: PdfProgress) => {
    setPercent(p.percent)
    setPageState({ current: p.page, total: p.totalPages })
    queueProgress(p.percent, encodeLocator({ kind: "page", page: p.page }))
  }

  const closePanels = () => {
    setTocOpen(false)
    setNotesOpen(false)
    setTypePanelOpen(false)
  }

  const exit = () => {
    // Write the position before unmounting rather than relying on the
    // unmount backstop, which fires mid-teardown.
    flushProgress()
    void navigate({ to: "/book/$id", params: { id: book.id } })
  }

  // Derived footer values — keep both reader shapes on one code path.
  const footerPageLabel =
    book.format === "PDF" && pageState
      ? `p.${pageState.current}`
      : book.format === "EPUB" && percent
        ? `${Math.round(percent * 100)}%`
        : "—"
  const footerTotalLabel =
    book.format === "PDF" && pageState ? `p.${pageState.total}` : ""

  return (
    <div
      className="fade-in"
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--color-paper-0)",
        zIndex: 200,
        display: "flex",
        flexDirection: "column",
      }}
    >
      {chromeVisible && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            padding: "10px 22px",
            borderBottom: "1px solid var(--color-rule-soft)",
            background: "var(--color-paper-1)",
          }}
        >
          <Button variant="ghost" size="sm" onClick={exit}>
            <Icon name="arrow-left" size={14} /> Library
          </Button>
          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
            }}
          >
            <div style={{ fontSize: 13, fontWeight: 500, fontStyle: "italic" }}>
              {book.title}
            </div>
            <div className="t-micro" style={{ fontSize: 10 }}>
              {book.author} · {footerPageLabel}
              {footerTotalLabel ? ` / ${footerTotalLabel}` : ""}
            </div>
          </div>
          {book.format === "EPUB" && (
            <Button
              variant={tocOpen ? "default" : "ghost"}
              size="icon-sm"
              onClick={() => {
                const next = !tocOpen
                closePanels()
                setTocOpen(next)
              }}
            >
              <Icon name="contents" size={14} />
            </Button>
          )}
          <Button
            variant={typePanelOpen ? "default" : "ghost"}
            size="icon-sm"
            onClick={() => {
              const next = !typePanelOpen
              closePanels()
              setTypePanelOpen(next)
            }}
          >
            <Icon name="aA" size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Bookmark"
            disabled={bookmarkMut.isPending}
            onClick={onBookmark}
          >
            <Icon name="bookmark" size={14} />
          </Button>
          <Button
            variant={notesOpen ? "default" : "ghost"}
            size="icon-sm"
            onClick={() => {
              const next = !notesOpen
              closePanels()
              setNotesOpen(next)
            }}
          >
            <Icon name="note" size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setChromeVisible(false)}
            title="Hide chrome"
          >
            <Icon name="close" size={14} />
          </Button>
        </div>
      )}

      <div
        style={{
          flex: 1,
          display: "flex",
          overflow: "hidden",
          position: "relative",
        }}
      >
        {/* Left TOC (EPUB only) */}
        {tocOpen && book.format === "EPUB" && (
          <aside
            style={{
              width: 280,
              borderRight: "1px solid var(--color-rule-soft)",
              background: "var(--color-paper-1)",
              overflow: "auto",
              padding: "18px 0",
              flexShrink: 0,
            }}
          >
            <div className="t-label" style={{ padding: "0 20px 10px" }}>
              Contents
            </div>
            {toc.length === 0 && (
              <div
                className="t-small"
                style={{ padding: "0 20px", fontStyle: "italic" }}
              >
                Table of contents not available.
              </div>
            )}
            {toc.map((c, i) => (
              <button
                // biome-ignore lint/suspicious/noArrayIndexKey: a TOC may point at the same href twice, so href alone is not unique; position disambiguates
                key={`${c.href}-${i}`}
                onClick={() => {
                  epubRef.current?.goTo(c.href)
                  setTocOpen(false)
                }}
                style={{
                  display: "block",
                  padding: "8px 20px",
                  width: "100%",
                  textAlign: "left",
                  border: "none",
                  background: "transparent",
                  fontFamily: "var(--font-serif)",
                  fontSize: 13.5,
                  color: "var(--color-ink-2)",
                  cursor: "pointer",
                  borderLeft: "2px solid transparent",
                }}
              >
                {c.label}
              </button>
            ))}
          </aside>
        )}

        {/* Reading area */}
        <div
          onClick={() => setChromeVisible(true)}
          style={{
            flex: 1,
            overflow: "hidden",
            position: "relative",
            background:
              book.format === "EPUB"
                ? "var(--color-paper-0)"
                : "var(--color-paper-2)",
          }}
        >
          {book.format === "EPUB" ? (
            <EpubReader
              ref={epubRef}
              url={`/api/v1/books/${book.id}/file`}
              initialCfi={resumeCfi}
              highlights={epubHighlights}
              onReady={({ toc: t }) => setToc(t.flatMap(flatten))}
              onProgress={onEpubProgress}
              onSelect={(sel) => setPendingSelection(sel)}
            />
          ) : (
            <PdfReader
              ref={pdfRef}
              url={`/api/v1/books/${book.id}/file`}
              initialPage={resumePage}
              onReady={({ totalPages }) =>
                setPageState({ current: resumePage ?? 1, total: totalPages })
              }
              onProgress={onPdfProgress}
            />
          )}

          {/* Selection toolbar — shown whenever the user drags across
              EPUB text and epub.js emits a `selected` event. Pending
              selection is cleared on save or dismiss. */}
          {pendingSelection && (
            <div
              style={{
                position: "absolute",
                top: 16,
                left: "50%",
                transform: "translateX(-50%)",
                zIndex: 10,
                display: "flex",
                alignItems: "center",
                gap: 10,
                padding: "8px 14px",
                background: "var(--color-paper-0)",
                border: "1px solid var(--color-ink-3)",
                borderRadius: 3,
                boxShadow: "0 6px 20px -4px oklch(0.2 0.02 60 / 0.22)",
              }}
            >
              <span className="t-micro">Selection</span>
              <Button
                type="button"
                size="sm"
                disabled={createAnnotationMut.isPending}
                onClick={() =>
                  createAnnotationMut.mutate({
                    bookId: book.id,
                    body: {
                      locator: pendingSelection.cfiRange,
                      selectedText: pendingSelection.text,
                    },
                  })
                }
              >
                <Icon name="highlight" size={12} /> Highlight
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={createAnnotationMut.isPending}
                onClick={() => {
                  const note = window.prompt("Add a note for this selection:")
                  if (!note?.trim()) return
                  createAnnotationMut.mutate({
                    bookId: book.id,
                    body: {
                      locator: pendingSelection.cfiRange,
                      selectedText: pendingSelection.text,
                      note: note.trim(),
                    },
                  })
                }}
              >
                <Icon name="note" size={12} /> Note
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={() => setPendingSelection(null)}
                aria-label="Dismiss"
              >
                <Icon name="close" size={12} />
              </Button>
            </div>
          )}
        </div>

        {/* Type panel (floating) */}
        {typePanelOpen && (
          <div
            style={{
              position: "absolute",
              top: 0,
              right: 16,
              width: 260,
              background: "var(--color-paper-0)",
              border: "1px solid var(--color-ink-3)",
              boxShadow: "0 12px 32px -8px oklch(0.2 0.02 60 / 0.22)",
              padding: "14px 16px",
              borderRadius: 2,
              zIndex: 5,
            }}
          >
            <div className="t-label" style={{ marginBottom: 10 }}>
              Reader type
            </div>
            <div
              style={{
                fontSize: 12,
                color: "var(--color-ink-3)",
                fontStyle: "italic",
              }}
            >
              Font + size controls land once per-user reader preferences sync
              from the backend.
            </div>
          </div>
        )}

        {notesOpen && (
          <aside
            style={{
              width: 320,
              borderLeft: "1px solid var(--color-rule-soft)",
              background: "var(--color-paper-1)",
              overflow: "auto",
              padding: "18px 16px",
              flexShrink: 0,
              display: "flex",
              flexDirection: "column",
              gap: 10,
            }}
          >
            <div className="t-label">Notes on this book</div>

            {book.format === "PDF" && pageState && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="w-full"
                disabled={createAnnotationMut.isPending}
                onClick={() => {
                  const note = window.prompt(
                    `Note on page ${pageState.current}:`
                  )
                  if (!note?.trim()) return
                  createAnnotationMut.mutate({
                    bookId: book.id,
                    body: {
                      locator: encodeLocator({
                        kind: "page",
                        page: pageState.current,
                      }),
                      note: note.trim(),
                    },
                  })
                }}
              >
                <Icon name="plus" size={12} /> New note on page{" "}
                {pageState.current}
              </Button>
            )}

            {annotations.isLoading && (
              <div className="t-small" style={{ fontStyle: "italic" }}>
                Loading…
              </div>
            )}
            {!annotations.isLoading &&
              (annotations.data ?? []).length === 0 && (
                <div className="t-small" style={{ fontStyle: "italic" }}>
                  {book.format === "EPUB"
                    ? "Select text in the page to highlight or annotate."
                    : "No notes yet."}
                </div>
              )}

            {(annotations.data ?? []).map((a) => {
              const kind = annotationKind(a)
              const locator = decodeLocator(a.locator)
              return (
                <div
                  key={a.id}
                  style={{
                    borderLeft: "3px solid var(--color-accent-soft)",
                    padding: "6px 10px",
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
                    <span className="t-micro" style={{ fontSize: 9.5 }}>
                      {kind === "highlight"
                        ? "Highlight"
                        : kind === "highlight+note"
                          ? "Highlight · Note"
                          : "Note"}
                      {/* A CFI reduces to "EPUB", which says nothing the
                          reader you are already inside doesn't — so only
                          a page carries a label worth showing here. */}
                      {locator?.kind === "page" &&
                        ` · ${locatorLabel(a.locator)}`}
                    </span>
                    <div
                      style={{ display: "flex", alignItems: "center", gap: 6 }}
                    >
                      {locator?.kind === "cfi" && book.format === "EPUB" && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          onClick={() => epubRef.current?.goTo(locator.cfi)}
                          title="Go to highlight"
                        >
                          <Icon name="arrow-right" size={10} />
                        </Button>
                      )}
                      {locator?.kind === "page" && book.format === "PDF" && (
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-xs"
                          // Decoding already rejected an unreadable page,
                          // so there is nothing left to re-validate here.
                          onClick={() => pdfRef.current?.goTo(locator.page)}
                          title="Go to page"
                        >
                          <Icon name="arrow-right" size={10} />
                        </Button>
                      )}
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        onClick={() =>
                          deleteAnnotationMut.mutate({
                            id: a.id,
                            bookId: a.bookId,
                          })
                        }
                        disabled={deleteAnnotationMut.isPending}
                        aria-label="Delete"
                        title="Delete"
                      >
                        <Icon name="close" size={10} />
                      </Button>
                    </div>
                  </div>
                  {a.selectedText && (
                    <p
                      style={{
                        fontSize: 12.5,
                        lineHeight: 1.5,
                        fontStyle: "italic",
                        background: "oklch(0.94 0.04 85)",
                        padding: "4px 6px",
                        marginBottom: a.note ? 6 : 0,
                      }}
                    >
                      {a.selectedText}
                    </p>
                  )}
                  {a.note && (
                    <p style={{ fontSize: 13, lineHeight: 1.5 }}>{a.note}</p>
                  )}
                </div>
              )
            })}
          </aside>
        )}
      </div>

      {/* Bottom — progress + page controls */}
      <div
        style={{
          borderTop: "1px solid var(--color-rule-soft)",
          padding: "10px 22px",
          display: "flex",
          alignItems: "center",
          gap: 14,
          background: "var(--color-paper-1)",
        }}
      >
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            book.format === "EPUB"
              ? epubRef.current?.prev()
              : pdfRef.current?.prev()
          }
        >
          <Icon name="chevron-left" size={14} /> Prev
        </Button>
        <div
          style={{ flex: 1, display: "flex", alignItems: "center", gap: 12 }}
        >
          <span
            className="mono"
            style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
          >
            {footerPageLabel}
          </span>
          <ProgressBar value={percent} label="Reading progress" />
          <span
            className="mono"
            style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
          >
            {footerTotalLabel || `${Math.round(percent * 100)}%`}
          </span>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() =>
            book.format === "EPUB"
              ? epubRef.current?.next()
              : pdfRef.current?.next()
          }
        >
          Next <Icon name="chevron-right" size={14} />
        </Button>
      </div>

      {!chromeVisible && (
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => setChromeVisible(true)}
          className="absolute top-2 right-2 z-10"
        >
          <Icon name="menu" size={14} />
        </Button>
      )}
    </div>
  )
}

// ComicReaderShell is the chrome around a CBZ ComicReader. It's deliberately
// a parallel implementation to ReaderShell rather than an extension: comics
// have no TOC, no text selection / annotations flow, and use a 0-indexed
// page model — folding all that into the existing shell would have made it
// significantly harder to read. The two shells share the progress
// debounce/persist pattern (queueProgress) but otherwise stand alone.
function ComicReaderShell({ book }: { book: BookDetail }) {
  const navigate = useNavigate()

  // ComicReader counts pages from 0; a page token carries the human page
  // number. The shell converts at its own boundary — the alternative,
  // storing this reader's indexing in the token, is what made `page:7`
  // mean p.8 in the reader chrome and p.7 in the notebook.
  const initialPage = useMemo(() => {
    const resume = decodeLocator(book.resumeCfi)
    return resume?.kind === "page" ? Math.max(0, resume.page - 1) : 0
  }, [book.resumeCfi])

  const [chromeVisible, setChromeVisible] = useState(true)
  const [fitMode, setFitMode] = useState<ComicFitMode>("page")
  const [page, setPage] = useState<number>(initialPage)
  const [total, setTotal] = useState<number>(0)
  const comicRef = useRef<ComicReaderHandle>(null)

  const { queue: queueProgress, flush: flushProgress } = useReadingPosition({
    save: (progress, token) => void updateProgress(book.id, progress, token),
    debounceMs: PROGRESS_DEBOUNCE_MS,
  })

  const bookmarkMut = useApiMutation(createBookmark, {
    successToast: "Bookmark saved",
    errorToast: (err) => err.message || "Bookmark failed",
  })

  const onComicProgress = (p: ComicProgress) => {
    setPage(p.page)
    setTotal(p.totalPages)
    queueProgress(p.percent, encodeLocator({ kind: "page", page: p.page + 1 }))
  }

  const exit = () => {
    // Write the position before unmounting rather than relying on the
    // unmount backstop, which fires mid-teardown.
    flushProgress()
    void navigate({ to: "/book/$id", params: { id: book.id } })
  }
  const percent = total <= 1 ? 1 : page / Math.max(1, total - 1)

  return (
    <div
      className="fade-in"
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--color-paper-2)",
        zIndex: 200,
        display: "flex",
        flexDirection: "column",
      }}
    >
      {chromeVisible && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 12,
            padding: "10px 22px",
            borderBottom: "1px solid var(--color-rule-soft)",
            background: "var(--color-paper-1)",
          }}
        >
          <Button variant="ghost" size="sm" onClick={exit}>
            <Icon name="arrow-left" size={14} /> Library
          </Button>
          <div
            style={{
              flex: 1,
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
            }}
          >
            <div style={{ fontSize: 13, fontWeight: 500, fontStyle: "italic" }}>
              {book.title}
            </div>
            <div className="t-micro" style={{ fontSize: 10 }}>
              {book.author} · p.{page + 1}
              {total > 0 ? ` / p.${total}` : ""}
            </div>
          </div>
          <div style={{ display: "flex", gap: 4 }}>
            <Button
              variant={fitMode === "page" ? "default" : "ghost"}
              size="sm"
              onClick={() => setFitMode("page")}
              title="Fit page"
            >
              Fit
            </Button>
            <Button
              variant={fitMode === "width" ? "default" : "ghost"}
              size="sm"
              onClick={() => setFitMode("width")}
              title="Fit width"
            >
              W
            </Button>
            <Button
              variant={fitMode === "height" ? "default" : "ghost"}
              size="sm"
              onClick={() => setFitMode("height")}
              title="Fit height"
            >
              H
            </Button>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Bookmark"
            disabled={bookmarkMut.isPending}
            onClick={() =>
              bookmarkMut.mutate({
                bookId: book.id,
                locator: { kind: "page", page: page + 1 },
              })
            }
          >
            <Icon name="bookmark" size={14} />
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setChromeVisible(false)}
            title="Hide chrome"
          >
            <Icon name="close" size={14} />
          </Button>
        </div>
      )}

      <div
        onDoubleClick={() => setChromeVisible((v) => !v)}
        style={{ flex: 1, position: "relative", overflow: "hidden" }}
      >
        <ComicReader
          ref={comicRef}
          bookId={book.id}
          initialPage={initialPage}
          fitMode={fitMode}
          onReady={({ totalPages }) => setTotal(totalPages)}
          onProgress={onComicProgress}
        />
      </div>

      <div
        style={{
          borderTop: "1px solid var(--color-rule-soft)",
          padding: "10px 22px",
          display: "flex",
          alignItems: "center",
          gap: 14,
          background: "var(--color-paper-1)",
        }}
      >
        <Button
          variant="ghost"
          size="sm"
          onClick={() => comicRef.current?.prev()}
        >
          <Icon name="chevron-left" size={14} /> Prev
        </Button>
        <div
          style={{ flex: 1, display: "flex", alignItems: "center", gap: 12 }}
        >
          <span
            className="mono"
            style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
          >
            p.{page + 1}
          </span>
          <ProgressBar
            value={percent}
            label="Page progress"
            onSeek={
              total > 0
                ? (fraction) =>
                    comicRef.current?.goTo(
                      Math.max(
                        0,
                        Math.min(total - 1, Math.round(fraction * (total - 1)))
                      )
                    )
                : undefined
            }
          />
          <span
            className="mono"
            style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
          >
            {total > 0 ? `p.${total}` : "—"}
          </span>
        </div>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => comicRef.current?.next()}
        >
          Next <Icon name="chevron-right" size={14} />
        </Button>
      </div>

      {!chromeVisible && (
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => setChromeVisible(true)}
          className="absolute top-2 right-2 z-10"
        >
          <Icon name="menu" size={14} />
        </Button>
      )}
    </div>
  )
}

// AudioReaderShell wraps AudioReader with the chrome an audiobook listener
// expects: cover + title at the top, big play/pause + skip controls
// in the middle, scrubber + chapter list at the bottom. Like
// ComicReaderShell, this is deliberately a parallel implementation
// instead of folded into ReaderShell — audio has a fundamentally
// different progress model (continuous time vs. discrete pages/CFIs).
function AudioReaderShell({
  book,
  audioUrl,
}: {
  book: BookDetail
  // Set for a narrated EPUB, whose audio is the *other* rendition and so
  // is not what the plain file route serves. Absent for a book that is
  // itself an audiobook.
  audioUrl?: string
}) {
  const navigate = useNavigate()

  const initialSeconds = useMemo(() => {
    const resume = decodeLocator(book.resumeCfi)
    return resume?.kind === "time" ? Math.max(0, resume.seconds) : 0
  }, [book.resumeCfi])

  const [seconds, setSeconds] = useState(initialSeconds)
  const [duration, setDuration] = useState(book.durationSeconds ?? 0)
  const [playing, setPlaying] = useState(false)
  const [rate, setRate] = useState(1)
  const [chapterIndex, setChapterIndex] = useState(-1)
  const [chaptersOpen, setChaptersOpen] = useState(false)
  const audioRef = useRef<AudioReaderHandle>(null)

  const { queue: queueProgress, flush: flushProgress } = useReadingPosition({
    save: (progress, token) => void updateProgress(book.id, progress, token),
    debounceMs: AUDIO_PROGRESS_DEBOUNCE_MS,
  })

  const bookmarkMut = useApiMutation(createBookmark, {
    successToast: "Bookmark saved",
    errorToast: (err) => err.message || "Bookmark failed",
  })

  const onAudioProgress = (p: AudioProgress) => {
    setSeconds(p.seconds)
    setDuration(p.duration)
    queueProgress(
      p.percent,
      encodeLocator({ kind: "time", seconds: p.seconds })
    )
  }

  const exit = () => {
    flushProgress()
    void navigate({ to: "/book/$id", params: { id: book.id } })
  }

  // No optimistic state update and no flush here: toggling the element makes
  // it fire play/pause, and onPlayingChange owns both. Keeping one path means
  // an in-page press and a headphone press behave identically.
  const togglePlay = () => audioRef.current?.toggle()

  const percent = duration > 0 ? seconds / duration : 0

  return (
    <div
      className="fade-in"
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--color-paper-1)",
        zIndex: 200,
        display: "flex",
        flexDirection: "column",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 12,
          padding: "10px 22px",
          borderBottom: "1px solid var(--color-rule-soft)",
          background: "var(--color-paper-1)",
        }}
      >
        <Button variant="ghost" size="sm" onClick={exit}>
          <Icon name="arrow-left" size={14} /> Library
        </Button>
        <div style={{ flex: 1 }} />
        <Button
          variant={chaptersOpen ? "default" : "ghost"}
          size="icon-sm"
          disabled={!book.chapters || book.chapters.length === 0}
          onClick={() => setChaptersOpen((v) => !v)}
          title="Chapters"
        >
          <Icon name="contents" size={14} />
        </Button>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Bookmark"
          disabled={bookmarkMut.isPending}
          onClick={() =>
            bookmarkMut.mutate({
              bookId: book.id,
              locator: { kind: "time", seconds },
            })
          }
        >
          <Icon name="bookmark" size={14} />
        </Button>
      </div>

      <div
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: 32,
          gap: 32,
          overflow: "auto",
        }}
      >
        {/* Cover */}
        <div
          style={{
            width: 280,
            height: 280,
            background: book.hasCover
              ? `var(--color-paper-3) url(/api/v1/books/${book.id}/cover) center / cover no-repeat`
              : "var(--color-paper-3)",
            borderRadius: 6,
            boxShadow: "0 12px 36px -8px oklch(0.2 0.02 60 / 0.32)",
            flexShrink: 0,
          }}
        />

        {/* Title block + transport + chapter list */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 14,
            maxWidth: 480,
            width: "100%",
          }}
        >
          <div>
            <div
              style={{
                fontSize: 24,
                fontStyle: "italic",
                fontWeight: 500,
                lineHeight: 1.2,
              }}
            >
              {book.title}
            </div>
            <div className="t-small" style={{ marginTop: 4 }}>
              {book.author}
              {book.narrator && book.narrator !== book.author
                ? ` · narr. ${book.narrator}`
                : ""}
            </div>
            {chapterIndex >= 0 && book.chapters?.[chapterIndex] && (
              <div
                className="t-micro"
                style={{ marginTop: 6, fontStyle: "italic" }}
              >
                {book.chapters[chapterIndex].title}
              </div>
            )}
          </div>

          {/* Big transport row: skip back, play/pause, skip forward, rate. */}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 14,
              padding: "8px 0",
            }}
          >
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => audioRef.current?.skip(-15)}
              title="Back 15s"
            >
              <Icon name="chevron-left" size={16} />
            </Button>
            <Button
              variant="default"
              size="lg"
              onClick={togglePlay}
              style={{ minWidth: 72 }}
            >
              <Icon name={playing ? "pause" : "play"} size={18} />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => audioRef.current?.skip(30)}
              title="Forward 30s"
            >
              <Icon name="chevron-right" size={16} />
            </Button>
            <div style={{ flex: 1 }} />
            <select
              value={rate}
              onChange={(e) => {
                const r = Number.parseFloat(e.target.value)
                setRate(r)
                audioRef.current?.setRate(r)
              }}
              className="mono"
              style={{
                fontSize: 12,
                padding: "4px 8px",
                background: "var(--color-paper-0)",
                border: "1px solid var(--color-ink-3)",
                borderRadius: 2,
              }}
            >
              {[0.75, 1, 1.25, 1.5, 1.75, 2].map((r) => (
                <option key={r} value={r}>
                  {r}×
                </option>
              ))}
            </select>
          </div>

          {/* Scrubber */}
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <span
              className="mono"
              style={{
                fontSize: 11,
                color: "var(--color-ink-3)",
                minWidth: 50,
              }}
            >
              {formatHMS(seconds)}
            </span>
            <ProgressBar
              value={percent}
              label="Listening progress"
              onSeek={
                duration > 0
                  ? (fraction) =>
                      audioRef.current?.seekTo(
                        Math.max(0, Math.min(duration, fraction * duration))
                      )
                  : undefined
              }
            >
              {/* Chapter tick marks — the one bar with children. */}
              {book.chapters?.map((c, i) => {
                if (duration <= 0) return null
                const left = (c.startS / duration) * 100
                if (left < 0 || left > 100) return null
                return (
                  <div
                    // biome-ignore lint/suspicious/noArrayIndexKey: markers are positioned from a derived percentage — the index is the marker
                    key={i}
                    style={{
                      position: "absolute",
                      top: -2,
                      left: `${left}%`,
                      width: 1,
                      height: 8,
                      background: "var(--color-ink-3)",
                      opacity: 0.4,
                    }}
                  />
                )
              })}
            </ProgressBar>
            <span
              className="mono"
              style={{
                fontSize: 11,
                color: "var(--color-ink-3)",
                minWidth: 50,
              }}
            >
              {formatHMS(duration)}
            </span>
          </div>

          {chaptersOpen && book.chapters && book.chapters.length > 0 && (
            <div
              style={{
                marginTop: 8,
                background: "var(--color-paper-0)",
                border: "1px solid var(--color-rule-soft)",
                borderRadius: 3,
                maxHeight: 240,
                overflow: "auto",
              }}
            >
              {book.chapters.map((c, i) => (
                <button
                  key={c.startS}
                  onClick={() => audioRef.current?.seekTo(c.startS)}
                  style={{
                    display: "block",
                    width: "100%",
                    textAlign: "left",
                    padding: "6px 12px",
                    border: "none",
                    background:
                      i === chapterIndex
                        ? "var(--color-paper-2)"
                        : "transparent",
                    fontFamily: "var(--font-serif)",
                    fontSize: 13,
                    color: "var(--color-ink-2)",
                    cursor: "pointer",
                  }}
                >
                  <span
                    className="mono"
                    style={{
                      marginRight: 10,
                      fontSize: 10.5,
                      color: "var(--color-ink-3)",
                    }}
                  >
                    {formatHMS(c.startS)}
                  </span>
                  {c.title}
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      <AudioReader
        ref={audioRef}
        url={audioUrl ?? `/api/v1/books/${book.id}/file`}
        initialSeconds={initialSeconds}
        initialRate={rate}
        chapters={book.chapters}
        title={book.title}
        author={book.author}
        artworkURL={
          book.hasCover ? `/api/v1/books/${book.id}/cover` : undefined
        }
        onReady={({ duration: d }) => setDuration(d)}
        onProgress={onAudioProgress}
        onChapterChange={(i) => setChapterIndex(i)}
        // Play state comes from the element, never from whoever pressed the
        // button: a headphone or lock-screen pause goes straight to the
        // media element via the Media Session handlers, so an optimistic
        // toggle here would leave the on-screen button showing the wrong
        // state until the next in-page interaction.
        onPlayingChange={(v) => {
          setPlaying(v)
          // Pausing from anywhere is the end of a listening session.
          if (!v) flushProgress()
        }}
      />
    </div>
  )
}

function FullScreenMessage({ children }: { children: React.ReactNode }) {
  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "var(--color-paper-0)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
    >
      <div className="t-small" style={{ textAlign: "center", maxWidth: 360 }}>
        {children}
      </div>
    </div>
  )
}

// flatten walks the EPUB TOC tree into a flat list for the simple linear
// Contents panel. Full-tree rendering is a future visual polish.
function flatten(node: EpubTocEntry): Array<TocItem> {
  const self: TocItem = { label: node.label, href: node.href }
  const sub = (node.subitems ?? []).flatMap(flatten)
  return [self, ...sub]
}
