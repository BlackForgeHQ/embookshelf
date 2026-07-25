import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react"
import * as pdfjsLib from "pdfjs-dist"
import type { PDFDocumentProxy } from "pdfjs-dist"

// Worker URL is computed with `new URL(..., import.meta.url)` so Vite emits
// the worker file as a hashed asset at build time and returns the resolved
// URL to the runtime. Doing this once at module load keeps the runtime
// hot-path clean.
pdfjsLib.GlobalWorkerOptions.workerSrc = new URL(
  "pdfjs-dist/build/pdf.worker.min.mjs",
  import.meta.url
).toString()

export type PdfProgress = {
  percent: number // 0..1
  page: number // 1-indexed
  totalPages: number
}

export type PdfReaderHandle = {
  next: () => void
  prev: () => void
  goTo: (page: number) => void
}

type Props = {
  url: string
  initialPage?: number
  onReady?: (meta: { totalPages: number }) => void
  onProgress?: (p: PdfProgress) => void
  onError?: (err: unknown) => void
}

// PdfReader paints each page into its own <canvas> inside a scroll
// container. Only the currently-visible page (plus one ahead / one behind)
// is rasterized at a time — keeps memory flat for large PDFs without
// needing a virtualized list.
export const PdfReader = forwardRef<PdfReaderHandle, Props>(
  function PdfReaderImpl(
    { url, initialPage, onReady, onProgress, onError },
    ref
  ) {
    const containerRef = useRef<HTMLDivElement | null>(null)
    const [docInfo, setDocInfo] = useState<{
      doc: PDFDocumentProxy
      total: number
    } | null>(null)
    const [currentPage, setCurrentPage] = useState(initialPage ?? 1)
    const initialScrollDone = useRef(false)

    useEffect(() => {
      // Explicit `: boolean` widens the type so the linter doesn't
      // narrow to literal-false — the cleanup closure mutates it.
      // `as boolean` widens the inferred literal-false so the
      // `if (cancelled)` guards below don't trip no-unnecessary-condition.
      let cancelled = false as boolean
      let loaded: PDFDocumentProxy | null = null

      ;(async () => {
        try {
          const loadingTask = pdfjsLib.getDocument({
            url,
            withCredentials: true,
          })
          const doc = await loadingTask.promise
          if (cancelled) {
            void loadingTask.destroy()
            return
          }
          loaded = doc
          setDocInfo({ doc, total: doc.numPages })
          onReady?.({ totalPages: doc.numPages })
        } catch (err) {
          if (!cancelled) onError?.(err)
        }
      })()

      return () => {
        cancelled = true
        void loaded?.loadingTask.destroy()
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [url])

    // Jump to initialPage once the container is mounted and doc is ready.
    useEffect(() => {
      if (!docInfo || initialScrollDone.current) return
      const target = Math.max(1, Math.min(docInfo.total, initialPage ?? 1))
      if (target > 1) {
        // Schedule after the page-placeholder layout pass so scrollTop is valid.
        requestAnimationFrame(() => scrollToPage(containerRef.current, target))
      }
      initialScrollDone.current = true
    }, [docInfo, initialPage])

    useImperativeHandle(
      ref,
      () => ({
        next: () => {
          if (!docInfo) return
          const next = Math.min(docInfo.total, currentPage + 1)
          scrollToPage(containerRef.current, next)
        },
        prev: () => {
          const next = Math.max(1, currentPage - 1)
          scrollToPage(containerRef.current, next)
        },
        goTo: (page: number) => {
          if (!docInfo) return
          const clamped = Math.max(1, Math.min(docInfo.total, page))
          scrollToPage(containerRef.current, clamped)
        },
      }),
      [docInfo, currentPage]
    )

    const pages = useMemo(() => {
      if (!docInfo) return null
      return Array.from({ length: docInfo.total }, (_, i) => i + 1)
    }, [docInfo])

    return (
      <div
        ref={containerRef}
        onScroll={(e) => {
          if (!docInfo) return
          const host = e.currentTarget
          const page = findVisiblePage(host, docInfo.total)
          if (page !== currentPage) {
            setCurrentPage(page)
            onProgress?.({
              page,
              totalPages: docInfo.total,
              percent: (page - 1) / Math.max(1, docInfo.total - 1),
            })
          }
        }}
        style={{
          width: "100%",
          height: "100%",
          overflow: "auto",
          background: "var(--color-paper-2)",
          padding: 24,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 16,
        }}
      >
        {pages === null && (
          <div
            className="t-small"
            style={{ marginTop: 40, fontStyle: "italic" }}
          >
            Loading PDF…
          </div>
        )}
        {pages &&
          docInfo &&
          pages.map((n) => (
            <PdfPage
              key={n}
              doc={docInfo.doc}
              pageNumber={n}
              active={n >= currentPage - 1 && n <= currentPage + 1}
              data-page={n}
            />
          ))}
      </div>
    )
  }
)

// PdfPage lazy-renders a single page. The placeholder preserves the
// approximate page height so the scroll container doesn't reflow when a
// real canvas finally paints.
function PdfPage({
  doc,
  pageNumber,
  active,
  ...rest
}: {
  doc: PDFDocumentProxy
  pageNumber: number
  active: boolean
} & Record<string, unknown>) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const [dims, setDims] = useState<{ w: number; h: number } | null>(null)

  // Resolve page dimensions once so the placeholder has the right height
  // before we commit to a real render.
  useEffect(() => {
    // `as boolean` widens the inferred literal-false so the
    // `if (cancelled)` guards below don't trip no-unnecessary-condition.
    let cancelled = false as boolean
    doc.getPage(pageNumber).then((page) => {
      if (cancelled) return
      const viewport = page.getViewport({ scale: 1.2 })
      setDims({ w: Math.round(viewport.width), h: Math.round(viewport.height) })
    })
    return () => {
      cancelled = true
    }
  }, [doc, pageNumber])

  // Render the canvas only while the page is within the active window
  // (current, current-1, current+1). Pages outside the window clear their
  // canvas to free memory.
  useEffect(() => {
    if (!canvasRef.current || !dims) return
    const canvas = canvasRef.current
    const ctx = canvas.getContext("2d")
    if (!ctx) return

    if (!active) {
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      return
    }

    // `as boolean` widens the inferred literal-false so the
    // `if (cancelled)` guards below don't trip no-unnecessary-condition.
    let cancelled = false as boolean
    let renderTask: ReturnType<PDFDocumentProxy["getPage"]> | null = null
    ;(async () => {
      const page = await doc.getPage(pageNumber)
      if (cancelled) return
      const viewport = page.getViewport({ scale: 1.2 })
      canvas.width = viewport.width
      canvas.height = viewport.height
      const task = page.render({ canvasContext: ctx, viewport, canvas })
      renderTask = task as never
      await task.promise
    })().catch(() => {
      // Cancelled or token expired — both are fine.
    })

    return () => {
      cancelled = true
      try {
        ;(renderTask as any)?.cancel?.()
      } catch {
        // ignore
      }
    }
  }, [active, doc, pageNumber, dims])

  return (
    <div
      ref={hostRef}
      {...rest}
      style={{
        width: dims?.w ?? 600,
        minHeight: dims?.h ?? 800,
        background: "var(--color-paper-0)",
        boxShadow: "0 2px 6px oklch(0.2 0.02 60 / 0.15)",
        display: "flex",
        alignItems: "flex-start",
        justifyContent: "center",
      }}
    >
      <canvas ref={canvasRef} style={{ display: "block", maxWidth: "100%" }} />
    </div>
  )
}

function scrollToPage(container: HTMLDivElement | null, page: number) {
  if (!container) return
  const el = container.querySelector<HTMLElement>(`[data-page="${page}"]`)
  if (el) {
    el.scrollIntoView({ behavior: "auto", block: "start" })
  }
}

function findVisiblePage(container: HTMLDivElement, total: number): number {
  const hosts = container.querySelectorAll<HTMLElement>("[data-page]")
  const hostRect = container.getBoundingClientRect()
  // First host whose top edge is below the container top is the active
  // page. Fallback to the last rendered page when the user scrolls past
  // the end of the document.
  for (const host of Array.from(hosts)) {
    const rect = host.getBoundingClientRect()
    if (rect.bottom >= hostRect.top + 40) {
      const n = Number(host.dataset.page)
      if (Number.isFinite(n)) return Math.max(1, Math.min(total, n))
    }
  }
  return total
}
