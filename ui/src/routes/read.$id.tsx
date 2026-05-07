import { useEffect, useMemo, useRef, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { toast } from "sonner"

import type { ApiError } from "@/api/client"
import type { Annotation } from "@/api/annotations"
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
import {
  annotationKind,
  bookAnnotationsQueryKey,
  createAnnotation,
  deleteAnnotation,
  fetchBookAnnotations,
  recentAnnotationsQueryKey,
} from "@/api/annotations"
import { bookQueryKey, fetchBook, updateProgress } from "@/api/books"
import { AudioReader } from "@/components/AudioReader"
import { ComicReader } from "@/components/ComicReader"
import { EpubReader } from "@/components/EpubReader"
import { Icon } from "@/components/Icon"
import { PdfReader } from "@/components/PdfReader"
import { Button } from "@/components/ui/button"

export const Route = createFileRoute("/read/$id")({
  component: Reader,
})

type TocItem = { label: string; href: string }

// parseResumeToken separates the two resume-token shapes we store in the
// database: raw CFI strings (EPUB) and page:N tokens (PDF). Unknown tokens
// fall back to "start from the beginning".
function parseResumeToken(raw?: string): {
  cfi?: string
  page?: number
  seconds?: number
} {
  if (!raw) return {}
  if (raw.startsWith("page:")) {
    const page = Number.parseInt(raw.slice(5), 10)
    return Number.isFinite(page) ? { page } : {}
  }
  if (raw.startsWith("time:")) {
    const seconds = Number.parseFloat(raw.slice(5))
    return Number.isFinite(seconds) ? { seconds } : {}
  }
  return { cfi: raw }
}

function Reader() {
  const { id } = Route.useParams()
  const navigate = useNavigate()

  const book = useQuery({
    queryKey: bookQueryKey(id),
    queryFn: () => fetchBook(id),
  })

  if (book.isLoading) {
    return <FullScreenMessage>Loading…</FullScreenMessage>
  }
  if (book.isError || !book.data) {
    return <FullScreenMessage>Book not found.</FullScreenMessage>
  }
  const b = book.data
  if (b.format === "CBZ") {
    return <ComicReaderShell book={b} />
  }
  if (b.format === "MP3" || b.format === "M4B") {
    return <AudioReaderShell book={b} />
  }
  if (b.format !== "EPUB" && b.format !== "PDF") {
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

  return <ReaderShell book={b} />
}

function ReaderShell({ book }: { book: BookDetail }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { cfi: resumeCfi, page: resumePage } = parseResumeToken(book.resumeCfi)

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

  const progressMut = useMutation({
    mutationFn: (args: { progress: number; resumeCfi: string }) =>
      updateProgress(book.id, args.progress, args.resumeCfi),
  })

  // Annotations for this book — drives the side panel AND the EPUB
  // highlight overlay.
  const annotations = useQuery({
    queryKey: bookAnnotationsQueryKey(book.id),
    queryFn: () => fetchBookAnnotations(book.id),
  })

  const invalidateAnnotations = () => {
    queryClient.invalidateQueries({
      queryKey: bookAnnotationsQueryKey(book.id),
    })
    queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey })
  }

  const createAnnotationMut = useMutation({
    mutationFn: (body: {
      locator?: string
      selectedText?: string
      note?: string
    }) => createAnnotation.fn({ bookId: book.id, body }),
    onSuccess: () => {
      invalidateAnnotations()
      setPendingSelection(null)
    },
  })

  const deleteAnnotationMut = useMutation({
    mutationFn: (a: Annotation) =>
      deleteAnnotation.fn({ id: a.id, bookId: a.bookId }),
    onSuccess: invalidateAnnotations,
  })

  // Bookmark = a zero-text annotation at the current location, marked
  // with color="bookmark" so the notebook can group it separately. The
  // annotations CHECK constraint requires selected_text or note to be
  // non-empty, so we put the literal label in selected_text.
  const bookmarkMut = useMutation({
    mutationFn: (locator: string) =>
      createAnnotation.fn({
        bookId: book.id,
        body: {
          locator,
          selectedText: "Bookmark",
          color: "bookmark",
        },
      }),
    onSuccess: () => {
      invalidateAnnotations()
      toast.success("Bookmark saved")
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Bookmark failed"),
  })

  const onBookmark = () => {
    const locator =
      book.format === "PDF" && pageState
        ? `page:${pageState.current}`
        : book.format === "EPUB" && cfiState
          ? cfiState
          : ""
    if (!locator) {
      toast.info("Open the book first, then bookmark.")
      return
    }
    bookmarkMut.mutate(locator)
  }

  // EPUB highlights for the rendition overlay. Stable reference when the
  // annotation list hasn't changed, so the effect in EpubReader doesn't
  // churn add/remove on every render.
  const epubHighlights = useMemo<Array<EpubHighlight>>(() => {
    if (book.format !== "EPUB") return []
    return (annotations.data ?? [])
      .filter((a) => !!a.selectedText && !!a.locator?.startsWith("epubcfi"))
      .map((a) => ({ cfiRange: a.locator!, color: "oklch(0.92 0.07 85)" }))
  }, [book.format, annotations.data])

  // Debounce + latest-wins: reader events fire every page turn; we hold
  // the newest tick for 600 ms and ship it, plus force a flush on unmount
  // so a short reading session still records progress.
  const pendingRef = useRef<{ progress: number; resumeCfi: string } | null>(
    null
  )
  const timerRef = useRef<number | null>(null)
  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current)
      if (pendingRef.current) {
        // Fire-and-forget — the component is already unmounting.
        void updateProgress(
          book.id,
          pendingRef.current.progress,
          pendingRef.current.resumeCfi
        )
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const queueProgress = (progress: number, cfi: string) => {
    pendingRef.current = { progress, resumeCfi: cfi }
    if (timerRef.current) window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => {
      const snapshot = pendingRef.current
      pendingRef.current = null
      timerRef.current = null
      if (snapshot) {
        progressMut.mutate(snapshot)
      }
    }, 600)
  }

  const onEpubProgress = (p: EpubProgress) => {
    setPercent(p.percent)
    setCfiState(p.cfi)
    queueProgress(p.percent, p.cfi)
  }
  const onPdfProgress = (p: PdfProgress) => {
    setPercent(p.percent)
    setPageState({ current: p.page, total: p.totalPages })
    queueProgress(p.percent, `page:${p.page}`)
  }

  const closePanels = () => {
    setTocOpen(false)
    setNotesOpen(false)
    setTypePanelOpen(false)
  }

  const exit = () => void navigate({ to: "/book/$id", params: { id: book.id } })

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
              onReady={({ toc: t }) => setToc(t.map(flatten).flat())}
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
                    locator: pendingSelection.cfiRange,
                    selectedText: pendingSelection.text,
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
                  if (!note || !note.trim()) return
                  createAnnotationMut.mutate({
                    locator: pendingSelection.cfiRange,
                    selectedText: pendingSelection.text,
                    note: note.trim(),
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
                  if (!note || !note.trim()) return
                  createAnnotationMut.mutate({
                    locator: `page:${pageState.current}`,
                    note: note.trim(),
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
                      {a.locator &&
                        a.locator.startsWith("page:") &&
                        ` · p.${a.locator.slice(5)}`}
                    </span>
                    <div
                      style={{ display: "flex", alignItems: "center", gap: 6 }}
                    >
                      {a.locator?.startsWith("epubcfi") &&
                        book.format === "EPUB" && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            onClick={() => epubRef.current?.goTo(a.locator!)}
                            title="Go to highlight"
                          >
                            <Icon name="arrow-right" size={10} />
                          </Button>
                        )}
                      {a.locator?.startsWith("page:") &&
                        book.format === "PDF" && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-xs"
                            onClick={() => {
                              const page = Number.parseInt(
                                a.locator!.slice(5),
                                10
                              )
                              if (Number.isFinite(page))
                                pdfRef.current?.goTo(page)
                            }}
                            title="Go to page"
                          >
                            <Icon name="arrow-right" size={10} />
                          </Button>
                        )}
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => deleteAnnotationMut.mutate(a)}
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
          <div
            style={{
              flex: 1,
              position: "relative",
              height: 4,
              background: "var(--color-paper-3)",
              borderRadius: 2,
            }}
          >
            <div
              style={{
                height: 4,
                width: `${Math.round(percent * 100)}%`,
                background: "var(--color-accent)",
                borderRadius: 2,
                transition: "width 120ms ease",
              }}
            />
          </div>
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
  const queryClient = useQueryClient()

  // Initial page (0-indexed) parsed out of the same `page:N` token
  // PDF readers persist; comics happen to also use 0-indexed pages
  // internally so we re-use the parser without translation.
  const initialPage = useMemo(() => {
    const t = parseResumeToken(book.resumeCfi)
    return typeof t.page === "number" ? Math.max(0, t.page) : 0
  }, [book.resumeCfi])

  const [chromeVisible, setChromeVisible] = useState(true)
  const [fitMode, setFitMode] = useState<ComicFitMode>("page")
  const [page, setPage] = useState<number>(initialPage)
  const [total, setTotal] = useState<number>(0)
  const comicRef = useRef<ComicReaderHandle>(null)

  const progressMut = useMutation({
    mutationFn: (args: { progress: number; resumeCfi: string }) =>
      updateProgress(book.id, args.progress, args.resumeCfi),
  })

  const bookmarkMut = useMutation({
    mutationFn: (locator: string) =>
      createAnnotation.fn({
        bookId: book.id,
        body: {
          locator,
          selectedText: `Bookmark · page ${page + 1}`,
          color: "bookmark",
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: bookAnnotationsQueryKey(book.id),
      })
      queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey })
      toast.success("Bookmark saved")
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Bookmark failed"),
  })

  // Progress persistence: same debounce shape as ReaderShell so a quick
  // session still records the last page on unmount.
  const pendingRef = useRef<{ progress: number; resumeCfi: string } | null>(
    null
  )
  const timerRef = useRef<number | null>(null)
  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current)
      if (pendingRef.current) {
        void updateProgress(
          book.id,
          pendingRef.current.progress,
          pendingRef.current.resumeCfi
        )
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const queueProgress = (progress: number, locator: string) => {
    pendingRef.current = { progress, resumeCfi: locator }
    if (timerRef.current) window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => {
      const snapshot = pendingRef.current
      pendingRef.current = null
      timerRef.current = null
      if (snapshot) progressMut.mutate(snapshot)
    }, 600)
  }

  const onComicProgress = (p: ComicProgress) => {
    setPage(p.page)
    setTotal(p.totalPages)
    queueProgress(p.percent, `page:${p.page}`)
  }

  const exit = () => void navigate({ to: "/book/$id", params: { id: book.id } })
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
            onClick={() => bookmarkMut.mutate(`page:${page}`)}
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
          <div
            style={{
              flex: 1,
              position: "relative",
              height: 4,
              background: "var(--color-paper-3)",
              borderRadius: 2,
              cursor: total > 0 ? "pointer" : "default",
            }}
            onClick={(e) => {
              if (total <= 0) return
              const rect = e.currentTarget.getBoundingClientRect()
              const ratio = (e.clientX - rect.left) / rect.width
              const target = Math.max(
                0,
                Math.min(total - 1, Math.round(ratio * (total - 1)))
              )
              comicRef.current?.goTo(target)
            }}
          >
            <div
              style={{
                height: 4,
                width: `${Math.round(percent * 100)}%`,
                background: "var(--color-accent)",
                borderRadius: 2,
                transition: "width 120ms ease",
              }}
            />
          </div>
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
function AudioReaderShell({ book }: { book: BookDetail }) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const initialSeconds = useMemo(() => {
    const t = parseResumeToken(book.resumeCfi)
    return typeof t.seconds === "number" ? Math.max(0, t.seconds) : 0
  }, [book.resumeCfi])

  const [seconds, setSeconds] = useState(initialSeconds)
  const [duration, setDuration] = useState(book.durationSeconds ?? 0)
  const [playing, setPlaying] = useState(false)
  const [rate, setRate] = useState(1)
  const [chapterIndex, setChapterIndex] = useState(-1)
  const [chaptersOpen, setChaptersOpen] = useState(false)
  const audioRef = useRef<AudioReaderHandle>(null)

  const progressMut = useMutation({
    mutationFn: (args: { progress: number; resumeCfi: string }) =>
      updateProgress(book.id, args.progress, args.resumeCfi),
  })

  const bookmarkMut = useMutation({
    mutationFn: (locator: string) =>
      createAnnotation.fn({
        bookId: book.id,
        body: {
          locator,
          selectedText: `Bookmark · ${formatHMS(seconds)}`,
          color: "bookmark",
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({
        queryKey: bookAnnotationsQueryKey(book.id),
      })
      queryClient.invalidateQueries({ queryKey: recentAnnotationsQueryKey })
      toast.success("Bookmark saved")
    },
    onError: (err) =>
      toast.error((err as unknown as ApiError).message || "Bookmark failed"),
  })

  // Same debounced-persist shape the other shells use, plus an
  // additional save-on-pause path so a quick listening session that
  // ends with the user just hitting pause still records position.
  const pendingRef = useRef<{ progress: number; resumeCfi: string } | null>(
    null
  )
  const timerRef = useRef<number | null>(null)
  const flush = () => {
    if (timerRef.current) window.clearTimeout(timerRef.current)
    timerRef.current = null
    const snap = pendingRef.current
    pendingRef.current = null
    if (snap) progressMut.mutate(snap)
  }
  useEffect(() => {
    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current)
      if (pendingRef.current) {
        void updateProgress(
          book.id,
          pendingRef.current.progress,
          pendingRef.current.resumeCfi
        )
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const queueProgress = (progress: number, locator: string) => {
    pendingRef.current = { progress, resumeCfi: locator }
    if (timerRef.current) window.clearTimeout(timerRef.current)
    timerRef.current = window.setTimeout(() => {
      flush()
    }, 5000) // longer debounce: time updates fire several times a second
  }

  const onAudioProgress = (p: AudioProgress) => {
    setSeconds(p.seconds)
    setDuration(p.duration)
    queueProgress(p.percent, `time:${p.seconds.toFixed(2)}`)
  }

  const exit = () => {
    flush()
    void navigate({ to: "/book/$id", params: { id: book.id } })
  }

  const togglePlay = () => {
    audioRef.current?.toggle()
    setPlaying((v) => !v)
  }

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
          onClick={() => bookmarkMut.mutate(`time:${seconds.toFixed(2)}`)}
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
            <div
              style={{
                flex: 1,
                position: "relative",
                height: 4,
                background: "var(--color-paper-3)",
                borderRadius: 2,
                cursor: duration > 0 ? "pointer" : "default",
              }}
              onClick={(e) => {
                if (duration <= 0) return
                const rect = e.currentTarget.getBoundingClientRect()
                const ratio = (e.clientX - rect.left) / rect.width
                audioRef.current?.seekTo(
                  Math.max(0, Math.min(duration, ratio * duration))
                )
              }}
            >
              <div
                style={{
                  height: 4,
                  width: `${Math.round(percent * 100)}%`,
                  background: "var(--color-accent)",
                  borderRadius: 2,
                  transition: "width 120ms linear",
                }}
              />
              {/* Chapter tick marks */}
              {book.chapters?.map((c, i) => {
                if (duration <= 0) return null
                const left = (c.startS / duration) * 100
                if (left < 0 || left > 100) return null
                return (
                  <div
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
            </div>
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
                  key={i}
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
        url={`/api/v1/books/${book.id}/file`}
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
      />
    </div>
  )
}

// formatHMS renders a seconds count as H:MM:SS (or M:SS for short
// clips). NaN / negative values render as "—:—".
function formatHMS(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—:—"
  const total = Math.floor(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const pad = (n: number) => n.toString().padStart(2, "0")
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
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
