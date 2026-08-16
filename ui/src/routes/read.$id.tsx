import { useMemo, useRef, useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"

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
import { runView } from "@/lib/audiobookRun"
import {
  CONTINUOUS_DEBOUNCE_MS,
  useReadingPosition,
} from "@/hooks/useReadingPosition"
import { useReaderShell } from "@/components/reader/useReaderShell"
import {
  isNarratableFormat,
  readerKindForFormat,
  renditionsFor,
} from "@/lib/formats"
import type { Rendition } from "@/lib/formats"
import { ProgressBar } from "@/components/ProgressBar"
import {
  BookmarkButton,
  ChromeRestoreButton,
  ReaderShellHeader,
  CollapsibleChrome,
  ReaderContainer,
  ReaderFooter,
} from "@/components/reader/Chrome"
import { decodeLocator, encodeLocator, formatHMS } from "@/lib/locator"
import { resumeCfi, resumePage, resumeSeconds } from "@/lib/resume"
import { NotesPanel, TypePanel } from "@/components/reader/Panels"
import {
  bookAnnotationsQuery,
  createAnnotation,
  deleteAnnotation,
} from "@/api/annotations"
import { bookAudiobookQuery, narrationUrl } from "@/api/audiobooks"
import { bookQuery } from "@/api/books"
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

type TocItem = { label: string; href: string }

function Reader() {
  const { id } = Route.useParams()
  const book = useApiQuery(bookQuery(id))

  if (book.isLoading) {
    return <FullScreenMessage>Loading…</FullScreenMessage>
  }
  if (book.isError || !book.data) {
    return <FullScreenMessage>Book not found.</FullScreenMessage>
  }
  return <RenditionReader book={book.data} />
}

/**
 * Dispatches on the Rendition the reader selected — the whole reader's
 * one decision about *what* to open (#179).
 *
 * ADR-0025 §3 moved the dispatch key off `books.format`, and this is
 * where that lands: the module answers which Renditions this book has
 * and which one was asked for, and the request falls back on its own
 * when a narration is not ready. Before, that was three decisions in
 * three components — a format check here, a narration check below it,
 * and a `listening && ready` guard inside the shell that owned neither.
 */
function RenditionReader({ book }: { book: BookDetail }) {
  const { id } = Route.useParams()
  const navigate = useNavigate()

  // Gated: a book nothing could have narrated has no run to fetch, and
  // opening a comic should not cost a request that 404s.
  const narration = useApiQuery(bookAudiobookQuery(book.id), {
    enabled: isNarratableFormat(book.format),
  })
  const [prefer, setPrefer] = useState<Rendition>("primary")

  // The wire row becomes derived facts here, where the query that
  // fetched it is: `renditionsFor` asks whether the narration is
  // playable and never sees a state string (#243). Null is a book that
  // has never been narrated — no run, so nothing to view — which is a
  // different question from the run's state and stays out here.
  const run = narration.data ? runView(narration.data) : null

  const rendition = renditionsFor(book.format, run, prefer)

  if (rendition.selected === null) {
    return (
      <FullScreenMessage>
        Reader not implemented for <code>{book.format}</code> yet.
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

  return (
    <>
      {rendition.canSwitch && (
        <RenditionSwitch
          listening={rendition.selected === "narration"}
          onChange={(listening) => setPrefer(listening ? "narration" : "primary")}
        />
      )}
      {rendition.selected === "narration" ? (
        <AudioReaderShell book={book} audioUrl={narrationUrl(book.id)} />
      ) : (
        <PrimaryShell book={book} />
      )}
    </>
  )
}

/**
 * Which renderer opens the book's own file.
 *
 * A different axis from the Rendition above, and deliberately still its
 * own dispatch: "text or narration" is what the reader chose, "EPUB or
 * PDF or comic" is what the file is. Collapsing them would put the drift
 * ADR-0025 §3 predicted back into the one place that had just been
 * cleared of it.
 */
function PrimaryShell({ book }: { book: BookDetail }) {
  switch (readerKindForFormat(book.format)) {
    case "comic":
      return <ComicReaderShell book={book} />
    case "audio":
      return <AudioReaderShell book={book} />
    default:
      // Both paged surfaces; the shells themselves have no format
      // branch (ADR-0029 §3).
      return book.format === "EPUB" ? (
        <TextReaderShell book={book} />
      ) : (
        <PdfReaderShell book={book} />
      )
  }
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

// TextReaderShell is the chrome around an EPUB.
//
// It and PdfReaderShell were one component until ADR-0029 §3: 8 of that
// component's 17 hooks served exactly one of the two formats, two
// imperative refs were allocated on every mount so one of them could be
// null for the component's lifetime, and sixteen render sites asked
// which format they were in. What was left once the branches were
// resolved is two shells that read straight through, sharing the chrome
// (`components/reader/Chrome.tsx`) and the two side panels
// (`components/reader/Panels.tsx`) that genuinely do not vary.
//
// What is EPUB-only and therefore lives here: the table of contents, the
// selection toolbar, the highlight overlay, and a position named by CFI.
function TextReaderShell({ book }: { book: BookDetail }) {
  // Read through `lib/resume`, which owns both stored positions and
  // hands back a string: EpubReader's boot effect has deps
  // `[url, initialCfi]`, so a decoded `Locator` would re-boot epub.js on
  // every render.
  const initialCfi = resumeCfi(book)

  // Chrome state, panel exclusivity and the bookmark mutation are the
  // shell module's (#351); what stays here is genuinely this shell's.
  const shell = useReaderShell(book.id)
  const [toc, setToc] = useState<Array<TocItem>>([])

  // epub.js unzips and lays the book out in-browser, which takes long
  // enough on a real EPUB that the reader sat on blank paper with no
  // sign anything was happening. "loading" until onReady, "error" if the
  // boot throws.
  const [bookState, setBookState] = useState<"loading" | "ready" | "error">(
    "loading"
  )

  // Progress state mirrors what the reader reports. Used for the bottom
  // scrubber and to compose the token we persist on unmount.
  const [percent, setPercent] = useState(0)
  const [cfiState, setCfiState] = useState<string>(initialCfi ?? "")

  // Pending selection — set by rendition.on('selected'), cleared when
  // the user saves or dismisses it. Absence hides the selection toolbar,
  // so this doubles as the toolbar's visibility switch.
  const [pendingSelection, setPendingSelection] = useState<{
    cfiRange: string
    text: string
  } | null>(null)

  const epubRef = useRef<EpubReaderHandle>(null)

  const { report: reportPosition, exit } = useReadingPosition({
    bookId: book.id,
  })

  // Annotations for this book — drives the notes panel AND the highlight
  // overlay. It stays here, immediately above the memo that reads it,
  // rather than being fetched once above both shells: separating the two
  // reintroduces the add/remove churn that memo exists to prevent
  // (ADR-0029 §3). The PDF shell keeps its own.
  const annotations = useApiQuery(bookAnnotationsQuery(book.id))

  const createAnnotationMut = useApiMutation(createAnnotation, {
    onSuccess: () => setPendingSelection(null),
  })

  const deleteAnnotationMut = useApiMutation(deleteAnnotation)

  // Null until the reader has reported a position: the shell mounts
  // before the renderer knows where it is, and the paged shells are the
  // only ones with that gap — the module's bookmark treats null as a
  // no-op, the guard every shell used to write by hand.
  const bookmarkLocator: Locator | null = cfiState
    ? { kind: "cfi", cfi: cfiState }
    : null

  // Highlights for the rendition overlay. Stable reference when the
  // annotation list hasn't changed, so the effect in EpubReader doesn't
  // churn add/remove on every render.
  const epubHighlights = useMemo<Array<EpubHighlight>>(
    () =>
      (annotations.data ?? [])
        .filter(
          (a) => !!a.selectedText && decodeLocator(a.locator)?.kind === "cfi"
        )
        // Literal, not var(--color-highlight): the fill lands in the
        // epub.js overlay where the app's custom properties may not
        // resolve.
        .map((a) => ({ cfiRange: a.locator!, color: "oklch(0.92 0.07 85)" })),
    [annotations.data]
  )

  const onEpubProgress = (p: EpubProgress) => {
    setPercent(p.percent)
    setCfiState(p.cfi)
    reportPosition(p.percent, { kind: "cfi", cfi: p.cfi })
  }

  // Derived labels. An EPUB has no page count, so how far through it the
  // reader is *is* the position — the footer's right-hand label shows
  // the same percentage without the "not started yet" dash.
  const percentLabel = `${Math.round(percent * 100)}%`
  const positionLabel = percent ? percentLabel : "—"

  return (
    <ReaderContainer background="var(--color-paper-0)">
      <CollapsibleChrome visible={shell.chromeVisible}>
        <ReaderShellHeader
          onExit={exit}
          title={book.title}
          author={book.author}
          positionLabel={positionLabel}
          onHideChrome={shell.hideChrome}
        >
          <Button
            variant={shell.openPanel === "toc" ? "default" : "ghost"}
            size="icon-sm"
            aria-label="Table of contents"
            onClick={() => shell.togglePanel("toc")}
          >
            <Icon name="contents" size={14} />
          </Button>
          <Button
            variant={shell.openPanel === "type" ? "default" : "ghost"}
            size="icon-sm"
            aria-label="Typography settings"
            onClick={() => shell.togglePanel("type")}
          >
            <Icon name="aA" size={14} />
          </Button>
          <BookmarkButton
            locator={bookmarkLocator}
            pending={shell.bookmarkPending}
            onBookmark={shell.bookmark}
          />
          <Button
            variant={shell.openPanel === "notes" ? "default" : "ghost"}
            size="icon-sm"
            aria-label="Notes"
            onClick={() => shell.togglePanel("notes")}
          >
            <Icon name="note" size={14} />
          </Button>
        </ReaderShellHeader>
      </CollapsibleChrome>

      <div
        style={{
          flex: 1,
          display: "flex",
          overflow: "hidden",
          position: "relative",
        }}
      >
        {/* Left TOC. The one panel with no PDF counterpart: pdf.js
            exposes an outline, but nothing here has ever read it. */}
        {shell.openPanel === "toc" && (
          <aside
            className="panel-in-left"
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
                  shell.closePanels()
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

        {/* Reading area.

            Pre-existing: the chrome-restore gesture differs in kind from
            the comic shell's, which double-clicks to toggle. A single
            click can restore here because an EPUB renders into an iframe
            that swallows it, so only the letterbox margins respond.
            Left as-is — unifying the two is a behaviour change. */}
        <div
          onClick={shell.showChrome}
          style={{
            flex: 1,
            overflow: "hidden",
            position: "relative",
            background: "var(--color-paper-0)",
          }}
        >
          <EpubReader
            ref={epubRef}
            url={`/api/v1/books/${book.id}/file`}
            initialCfi={initialCfi}
            highlights={epubHighlights}
            onReady={({ toc: t }) => {
              setToc(t.flatMap(flatten))
              setBookState("ready")
            }}
            onProgress={onEpubProgress}
            onSelect={(sel) => setPendingSelection(sel)}
            onError={() => setBookState("error")}
          />

          {bookState !== "ready" && (
            <div
              style={{
                position: "absolute",
                inset: 0,
                display: "flex",
                alignItems: "center",
                justifyContent: "center",
                background: "var(--color-paper-0)",
              }}
            >
              <span
                className="t-small italic"
                style={{
                  color:
                    bookState === "error"
                      ? "var(--color-accent-ink)"
                      : "var(--color-ink-3)",
                }}
                role={bookState === "error" ? "alert" : "status"}
              >
                {bookState === "error"
                  ? "Couldn't open this book."
                  : "Opening the book…"}
              </span>
            </div>
          )}

          {/* Selection toolbar — shown whenever the user drags across
              text and epub.js emits a `selected` event. Pending
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

        {shell.openPanel === "type" && <TypePanel />}

        {shell.openPanel === "notes" && (
          <NotesPanel
            annotations={annotations.data ?? []}
            loading={annotations.isLoading}
            emptyText="Select text in the page to highlight or annotate."
            renderGoTo={(locator) =>
              locator.kind === "cfi" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => epubRef.current?.goTo(locator.cfi)}
                  title="Go to highlight"
                >
                  <Icon name="arrow-right" size={10} />
                </Button>
              ) : null
            }
            onDelete={(a) =>
              deleteAnnotationMut.mutate({ id: a.id, bookId: a.bookId })
            }
            deleting={deleteAnnotationMut.isPending}
          />
        )}
      </div>

      {/* Bottom — progress + page controls. Outside the collapsible
          chrome by the module's rule (#351): "hide chrome" hides the
          header, never the reading position. */}
      <ReaderFooter
        onPrev={() => epubRef.current?.prev()}
        onNext={() => epubRef.current?.next()}
        leftLabel={positionLabel}
        rightLabel={percentLabel}
      >
        <ProgressBar value={percent} label="Reading progress" />
      </ReaderFooter>

      {!shell.chromeVisible && (
        <ChromeRestoreButton onRestore={shell.showChrome} />
      )}
    </ReaderContainer>
  )
}

// PdfReaderShell is the chrome around a PDF — TextReaderShell's sibling
// rather than a variant of it (ADR-0029 §3).
//
// What is PDF-only and therefore lives here: a position named by page,
// so the header can say "p.7 / p.240" where the text shell can only say
// a percentage, and a notes panel whose new note attaches to the page on
// screen because there is no text selection to attach it to.
function PdfReaderShell({ book }: { book: BookDetail }) {
  // The human page number, which is what PdfReader's 1-based
  // `initialPage` wants. Undefined when the text position is a CFI, and
  // PdfReader then starts at p.1 (`lib/resume`).
  const initialPage = resumePage(book)

  const shell = useReaderShell(book.id)

  // Progress state mirrors what the reader reports. Used for the bottom
  // scrubber and to compose the token we persist on unmount.
  const [percent, setPercent] = useState(0)
  const [pageState, setPageState] = useState<{
    current: number
    total: number
  } | null>(null)

  const pdfRef = useRef<PdfReaderHandle>(null)

  const { report: reportPosition, exit } = useReadingPosition({
    bookId: book.id,
  })

  // This shell's own annotations query, for the notes panel. Not one
  // hoisted above both shells: a PDF has no highlight overlay, but the
  // text shell's list has to stay next to the memo that builds one
  // (ADR-0029 §3), so each shell asks for what it needs. Only one of the
  // two is ever mounted, so this is one fetch either way.
  const annotations = useApiQuery(bookAnnotationsQuery(book.id))

  const createAnnotationMut = useApiMutation(createAnnotation)

  const deleteAnnotationMut = useApiMutation(deleteAnnotation)

  // Null until the reader has reported a position; the module's
  // bookmark treats null as a no-op (#351).
  const bookmarkLocator: Locator | null = pageState
    ? { kind: "page", page: pageState.current }
    : null

  const onPdfProgress = (p: PdfProgress) => {
    setPercent(p.percent)
    setPageState({ current: p.page, total: p.totalPages })
    reportPosition(p.percent, { kind: "page", page: p.page })
  }

  // Derived labels. Both are empty-ish until the document reports its
  // page count, and the footer falls back to the percentage then.
  const pageLabel = pageState ? `p.${pageState.current}` : "—"
  const totalLabel = pageState ? `p.${pageState.total}` : ""

  return (
    <ReaderContainer background="var(--color-paper-0)">
      <CollapsibleChrome visible={shell.chromeVisible}>
        <ReaderShellHeader
          onExit={exit}
          title={book.title}
          author={book.author}
          positionLabel={`${pageLabel}${totalLabel ? ` / ${totalLabel}` : ""}`}
          onHideChrome={shell.hideChrome}
        >
          <Button
            variant={shell.openPanel === "type" ? "default" : "ghost"}
            size="icon-sm"
            aria-label="Typography settings"
            onClick={() => shell.togglePanel("type")}
          >
            <Icon name="aA" size={14} />
          </Button>
          <BookmarkButton
            locator={bookmarkLocator}
            pending={shell.bookmarkPending}
            onBookmark={shell.bookmark}
          />
          <Button
            variant={shell.openPanel === "notes" ? "default" : "ghost"}
            size="icon-sm"
            aria-label="Notes"
            onClick={() => shell.togglePanel("notes")}
          >
            <Icon name="note" size={14} />
          </Button>
        </ReaderShellHeader>
      </CollapsibleChrome>

      <div
        style={{
          flex: 1,
          display: "flex",
          overflow: "hidden",
          position: "relative",
        }}
      >
        {/* Reading area.

            Pre-existing: the single-click restore is inherited from the
            shell these two were split out of, where it was written for
            the EPUB iframe that swallows the click. A PDF canvas does
            not, so a click anywhere in the page restores chrome here.
            Left as-is — changing it is a behaviour change. */}
        <div
          onClick={shell.showChrome}
          style={{
            flex: 1,
            overflow: "hidden",
            position: "relative",
            background: "var(--color-paper-2)",
          }}
        >
          <PdfReader
            ref={pdfRef}
            url={`/api/v1/books/${book.id}/file`}
            initialPage={initialPage}
            onReady={({ totalPages }) =>
              setPageState({ current: initialPage ?? 1, total: totalPages })
            }
            onProgress={onPdfProgress}
          />
        </div>

        {shell.openPanel === "type" && <TypePanel />}

        {shell.openPanel === "notes" && (
          <NotesPanel
            annotations={annotations.data ?? []}
            loading={annotations.isLoading}
            emptyText="No notes yet."
            renderGoTo={(locator) =>
              locator.kind === "page" ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  // Decoding already rejected an unreadable page, so
                  // there is nothing left to re-validate here.
                  onClick={() => pdfRef.current?.goTo(locator.page)}
                  title="Go to page"
                >
                  <Icon name="arrow-right" size={10} />
                </Button>
              ) : null
            }
            onDelete={(a) =>
              deleteAnnotationMut.mutate({ id: a.id, bookId: a.bookId })
            }
            deleting={deleteAnnotationMut.isPending}
          >
            {pageState && (
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
          </NotesPanel>
        )}
      </div>

      {/* Bottom — progress + page controls. Outside the collapsible
          chrome by the module's rule (#351). */}
      <ReaderFooter
        onPrev={() => pdfRef.current?.prev()}
        onNext={() => pdfRef.current?.next()}
        leftLabel={pageLabel}
        rightLabel={totalLabel || `${Math.round(percent * 100)}%`}
      >
        <ProgressBar value={percent} label="Reading progress" />
      </ReaderFooter>

      {!shell.chromeVisible && (
        <ChromeRestoreButton onRestore={shell.showChrome} />
      )}
    </ReaderContainer>
  )
}

// ComicReaderShell is the chrome around a CBZ ComicReader. It's deliberately
// a parallel implementation to ReaderShell rather than an extension: comics
// have no TOC, no text selection / annotations flow, and use a 0-indexed
// page model — folding all that into the existing shell would have made it
// significantly harder to read. The two shells report their position
// through the same module but otherwise stand alone.
function ComicReaderShell({ book }: { book: BookDetail }) {
  // ComicReader counts pages from 0; `lib/resume` hands back the human
  // page number, as every other consumer of the token wants it. The -1
  // is this reader's indexing and so converts at this reader's boundary
  // — the alternative, storing the indexing in the token or in the
  // resume module, is what made `page:7` mean p.8 in the reader chrome
  // and p.7 in the notebook.
  const humanPage = resumePage(book)
  const initialPage = humanPage === undefined ? 0 : Math.max(0, humanPage - 1)

  const shell = useReaderShell(book.id)
  const [fitMode, setFitMode] = useState<ComicFitMode>("page")
  const [page, setPage] = useState<number>(initialPage)
  const [total, setTotal] = useState<number>(0)
  const comicRef = useRef<ComicReaderHandle>(null)

  const { report: reportPosition, exit } = useReadingPosition({
    bookId: book.id,
  })

  const onComicProgress = (p: ComicProgress) => {
    setPage(p.page)
    setTotal(p.totalPages)
    // +1 because this reader's page model is 0-indexed and a stored
    // locator carries the human page number (`lib/locator`).
    reportPosition(p.percent, { kind: "page", page: p.page + 1 })
  }

  const percent = total <= 1 ? 1 : page / Math.max(1, total - 1)

  return (
    <ReaderContainer background="var(--color-paper-2)">
      <CollapsibleChrome visible={shell.chromeVisible}>
        <ReaderShellHeader
          onExit={exit}
          title={book.title}
          author={book.author}
          positionLabel={`p.${page + 1}${total > 0 ? ` / p.${total}` : ""}`}
          onHideChrome={shell.hideChrome}
        >
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
          <BookmarkButton
            // Never null: the comic shell resumes to a page before it
            // mounts the reader, so there is always a page to point at.
            locator={{ kind: "page", page: page + 1 }}
            pending={shell.bookmarkPending}
            onBookmark={shell.bookmark}
          />
        </ReaderShellHeader>
      </CollapsibleChrome>

      {/* The one gesture is the module's restore button (#351); the
          double-click toggle stays as this shell's extra, because a
          comic has no text selection for it to fight and a single
          click is already a page turn. */}
      <div
        onDoubleClick={shell.toggleChrome}
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

      {/* Outside the collapsible chrome by the module's rule (#351). */}
      <ReaderFooter
        onPrev={() => comicRef.current?.prev()}
        onNext={() => comicRef.current?.next()}
        leftLabel={`p.${page + 1}`}
        rightLabel={total > 0 ? `p.${total}` : "—"}
      >
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
      </ReaderFooter>

      {!shell.chromeVisible && (
        <ChromeRestoreButton onRestore={shell.showChrome} />
      )}
    </ReaderContainer>
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
  // The narration's own position, not the text one: this book's two
  // Renditions keep two, because a CFI and a timestamp are different
  // currencies until the alignment map bridges them. Reading the shared
  // field is what lost the listener's place on every Read → Listen →
  // Read round trip (#200); `lib/resume` is where that split lives now.
  const initialSeconds = resumeSeconds(book) ?? 0

  const [seconds, setSeconds] = useState(initialSeconds)
  const [duration, setDuration] = useState(book.durationSeconds ?? 0)
  const [playing, setPlaying] = useState(false)
  const [rate, setRate] = useState(1)
  const [chapterIndex, setChapterIndex] = useState(-1)
  const shell = useReaderShell(book.id)
  const audioRef = useRef<AudioReaderHandle>(null)

  const {
    report: reportPosition,
    settle: settlePosition,
    exit,
  } = useReadingPosition({
    bookId: book.id,
    debounceMs: CONTINUOUS_DEBOUNCE_MS,
  })

  const onAudioProgress = (p: AudioProgress) => {
    setSeconds(p.seconds)
    setDuration(p.duration)
    reportPosition(p.percent, { kind: "time", seconds: p.seconds })
  }

  // No optimistic state update and no settle here: toggling the element makes
  // it fire play/pause, and onPlayingChange owns both. Keeping one path means
  // an in-page press and a headphone press behave identically.
  const togglePlay = () => audioRef.current?.toggle()

  const percent = duration > 0 ? seconds / duration : 0

  return (
    <ReaderContainer background="var(--color-paper-1)">
      {/* Audio joins the collapsible chrome (#351): it used to be the
          one shell with no chrome state at all — the third of the three
          preserved inconsistencies, resolved by adoption rather than
          preserved by comment. The transport below stays put; hiding
          chrome hides only this header, the module's rule. */}
      <CollapsibleChrome visible={shell.chromeVisible}>
        <ReaderShellHeader onExit={exit} onHideChrome={shell.hideChrome}>
          <Button
            variant={shell.openPanel === "chapters" ? "default" : "ghost"}
            size="icon-sm"
            disabled={!book.chapters || book.chapters.length === 0}
            onClick={() => shell.togglePanel("chapters")}
            title="Chapters"
            aria-label="Chapters"
          >
            <Icon name="contents" size={14} />
          </Button>
          <BookmarkButton
            // Never null: playback position starts at the resume offset, so
            // zero seconds is a real position rather than "not open yet".
            locator={{ kind: "time", seconds }}
            pending={shell.bookmarkPending}
            onBookmark={shell.bookmark}
          />
        </ReaderShellHeader>
      </CollapsibleChrome>

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
              aria-label="Back 15 seconds"
            >
              <Icon name="chevron-left" size={16} />
            </Button>
            <Button
              variant="default"
              size="lg"
              onClick={togglePlay}
              aria-label={playing ? "Pause" : "Play"}
              style={{ minWidth: 72 }}
            >
              <Icon name={playing ? "pause" : "play"} size={18} />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => audioRef.current?.skip(30)}
              title="Forward 30s"
              aria-label="Forward 30 seconds"
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

          {shell.openPanel === "chapters" && book.chapters && book.chapters.length > 0 && (
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
          if (!v) settlePosition()
        }}
      />

      {!shell.chromeVisible && (
        <ChromeRestoreButton onRestore={shell.showChrome} />
      )}
    </ReaderContainer>
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
