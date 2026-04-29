import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react"

import { comicPageURL, fetchComicPageCount } from "@/api/books"

export type ComicProgress = {
  percent: number // 0..1
  page: number // 0-indexed
  totalPages: number
}

export type ComicReaderHandle = {
  next: () => void
  prev: () => void
  goTo: (page: number) => void
}

export type ComicFitMode = "width" | "height" | "page"

type Props = {
  bookId: string
  initialPage?: number // 0-indexed
  fitMode?: ComicFitMode
  onReady?: (meta: { totalPages: number }) => void
  onProgress?: (p: ComicProgress) => void
  onError?: (err: unknown) => void
}

// ComicReader displays one page of a CBZ archive at a time. Pages are
// served lazily by the backend as image bytes from a streaming endpoint;
// we preload n+1 (and a tiny window backwards) so a normal page-turn is
// instant while not paying memory for the whole book up front.
//
// Progress = current page / (total - 1). Persisted as `page:N` (0-indexed)
// to mirror PDF's existing scheme.
export const ComicReader = forwardRef<ComicReaderHandle, Props>(
  function ComicReaderImpl(
    { bookId, initialPage, fitMode = "page", onReady, onProgress, onError },
    ref
  ) {
    const [total, setTotal] = useState<number | null>(null)
    const [page, setPage] = useState<number>(Math.max(0, initialPage ?? 0))
    const [loaded, setLoaded] = useState(false)
    const onProgressRef = useRef(onProgress)
    onProgressRef.current = onProgress

    // One-shot load of the page count.
    useEffect(() => {
      let cancelled = false as boolean
      ;(async () => {
        try {
          const count = await fetchComicPageCount(bookId)
          if (cancelled) return
          setTotal(count)
          onReady?.({ totalPages: count })
        } catch (err) {
          if (!cancelled) onError?.(err)
        }
      })()
      return () => {
        cancelled = true
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [bookId])

    // Clamp page once total is known. Initial page may have been beyond
    // a now-shorter archive (rare, but possible after a re-import).
    useEffect(() => {
      if (total === null) return
      const clamped = Math.max(0, Math.min(total - 1, page))
      if (clamped !== page) setPage(clamped)
      // Emit initial progress once we know the page is valid.
      if (total > 0) {
        onProgressRef.current?.({
          page: clamped,
          totalPages: total,
          percent: total <= 1 ? 1 : clamped / (total - 1),
        })
      }
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [total])

    const setAndEmit = (next: number) => {
      if (total === null) return
      const clamped = Math.max(0, Math.min(total - 1, next))
      if (clamped === page) return
      setPage(clamped)
      setLoaded(false)
      onProgressRef.current?.({
        page: clamped,
        totalPages: total,
        percent: total <= 1 ? 1 : clamped / (total - 1),
      })
    }

    useImperativeHandle(
      ref,
      () => ({
        next: () => setAndEmit(page + 1),
        prev: () => setAndEmit(page - 1),
        goTo: (n: number) => setAndEmit(n),
      }),
      // eslint-disable-next-line react-hooks/exhaustive-deps
      [page, total]
    )

    // Keyboard navigation: arrows, space, page-up/down. Bound on window so
    // it works even when the focus is on a button in the chrome.
    useEffect(() => {
      const onKey = (e: KeyboardEvent) => {
        if (e.target instanceof HTMLInputElement) return
        switch (e.key) {
          case "ArrowRight":
          case "PageDown":
          case " ":
            e.preventDefault()
            setAndEmit(page + 1)
            break
          case "ArrowLeft":
          case "PageUp":
            e.preventDefault()
            setAndEmit(page - 1)
            break
          case "Home":
            e.preventDefault()
            setAndEmit(0)
            break
          case "End":
            e.preventDefault()
            if (total !== null) setAndEmit(total - 1)
            break
        }
      }
      window.addEventListener("keydown", onKey)
      return () => window.removeEventListener("keydown", onKey)
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [page, total])

    // Preload neighbors. Browsers cache the GET responses, so subsequent
    // navigations to recently-seen pages are instant. We don't preload an
    // unbounded window — just ±2 around the current page.
    const preloadURLs = useMemo(() => {
      if (total === null) return []
      const out: Array<string> = []
      for (let d = 1; d <= 2; d++) {
        if (page + d < total) out.push(comicPageURL(bookId, page + d))
        if (page - d >= 0) out.push(comicPageURL(bookId, page - d))
      }
      return out
    }, [bookId, page, total])

    // Fit-mode CSS shape. "page" = whichever axis fills first; "width"
    // and "height" are explicit overrides for tall vs wide layouts.
    const imgStyle = useMemo<React.CSSProperties>(() => {
      switch (fitMode) {
        case "width":
          return { width: "100%", height: "auto", maxWidth: "100%" }
        case "height":
          return { height: "100%", width: "auto", maxHeight: "100%" }
        default:
          return {
            maxWidth: "100%",
            maxHeight: "100%",
            width: "auto",
            height: "auto",
          }
      }
    }, [fitMode])

    return (
      <div
        style={{
          width: "100%",
          height: "100%",
          background: "var(--color-paper-2)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          position: "relative",
          overflow: "hidden",
          userSelect: "none",
        }}
      >
        {total === null && (
          <div className="t-small" style={{ fontStyle: "italic" }}>
            Loading comic…
          </div>
        )}
        {total !== null && total === 0 && (
          <div className="t-small" style={{ fontStyle: "italic" }}>
            This archive contains no pages.
          </div>
        )}
        {total !== null && total > 0 && (
          <>
            {/* Click-to-navigate zones. Left half = prev, right half = next.
                Sits behind the image so the image still receives layout. */}
            <button
              aria-label="Previous page"
              onClick={() => setAndEmit(page - 1)}
              style={{
                position: "absolute",
                left: 0,
                top: 0,
                width: "30%",
                height: "100%",
                background: "transparent",
                border: "none",
                cursor: "w-resize",
                zIndex: 1,
              }}
            />
            <button
              aria-label="Next page"
              onClick={() => setAndEmit(page + 1)}
              style={{
                position: "absolute",
                right: 0,
                top: 0,
                width: "30%",
                height: "100%",
                background: "transparent",
                border: "none",
                cursor: "e-resize",
                zIndex: 1,
              }}
            />
            <img
              key={page}
              src={comicPageURL(bookId, page)}
              alt={`Page ${page + 1}`}
              draggable={false}
              onLoad={() => setLoaded(true)}
              onError={(e) => {
                setLoaded(true)
                onError?.(
                  new Error(`Failed to load page ${page}: ${e.type}`)
                )
              }}
              style={{
                ...imgStyle,
                objectFit: "contain",
                opacity: loaded ? 1 : 0,
                transition: "opacity 120ms ease",
                boxShadow: "0 2px 12px oklch(0.2 0.02 60 / 0.25)",
              }}
            />
            {!loaded && (
              <div
                className="t-small"
                style={{
                  position: "absolute",
                  fontStyle: "italic",
                  pointerEvents: "none",
                }}
              >
                Loading page {page + 1}…
              </div>
            )}
            {/* Hidden preloads. <link rel=preload> would be ideal but
                only works at document level; an off-screen <img> is the
                portable in-component equivalent. */}
            <div style={{ position: "absolute", width: 0, height: 0, overflow: "hidden" }}>
              {preloadURLs.map((u) => (
                <img key={u} src={u} alt="" />
              ))}
            </div>
          </>
        )}
      </div>
    )
  }
)
