import {
  forwardRef,
  useEffect,
  useImperativeHandle,
  useRef,
  useState,
} from "react"
import ePub from "epubjs"

export type EpubTocEntry = {
  label: string
  href: string
  subitems?: Array<EpubTocEntry>
}

export type EpubProgress = {
  percent: number // 0..1
  cfi: string
}

export type EpubReaderHandle = {
  next: () => void
  prev: () => void
  goTo: (href: string) => void
}

export type EpubHighlight = {
  cfiRange: string
  color?: string // CSS color (oklch(...) etc.); default is a warm highlight
}

type Props = {
  url: string
  initialCfi?: string
  fontFamily?: string
  fontSize?: number // px
  lineHeight?: number
  highlights?: Array<EpubHighlight>
  onReady?: (meta: { toc: Array<EpubTocEntry>; totalPages: number }) => void
  onProgress?: (p: EpubProgress) => void
  onSelect?: (selection: { cfiRange: string; text: string } | null) => void
  onError?: (err: unknown) => void
}

// EpubReader wraps epub.js and exposes an imperative handle for the
// surrounding chrome (prev/next buttons, TOC click-through) so the route
// can stay declarative. Rendering is paginated ("book-spread") inside a
// fixed-size container — the parent controls layout.
export const EpubReader = forwardRef<EpubReaderHandle, Props>(
  function EpubReaderImpl(
    {
      url,
      initialCfi,
      fontFamily,
      fontSize,
      lineHeight,
      highlights,
      onReady,
      onProgress,
      onSelect,
      onError,
    },
    ref
  ) {
    const containerRef = useRef<HTMLDivElement | null>(null)
    const [booted, setBooted] = useState(false)
    const renditionRef = useRef<any>(null)

    // biome-ignore lint/correctness/useExhaustiveDependencies: boots once per url; typography flows through a separate effect so a font change does not tear down the rendition
    useEffect(() => {
      if (!containerRef.current) return
      // Mutable flag captured by cleanup. Boxed in an object so
      // TS control-flow analysis doesn't narrow it across the
      // async waits below (otherwise no-unnecessary-condition
      // flags the guards as unreachable).
      const flag = { cancelled: false }
      let book: any
      let rendition: any
      ;(async () => {
        try {
          // epub.js guesses "directory" mode for URLs without a .epub
          // suffix and starts fetching META-INF/container.xml relative
          // to the parent path. Our /books/:id/file endpoint is a
          // binary zip — explicitly pass openAs: 'epub' so jszip
          // unpacks it in-browser.
          book = ePub(url, { openAs: "epub" })
          if (flag.cancelled) return

          rendition = book.renderTo(containerRef.current, {
            width: "100%",
            height: "100%",
            flow: "paginated",
            allowScriptedContent: true,
          })
          renditionRef.current = rendition

          // Typography overrides applied via themes so they survive page
          // transitions (inline style injection doesn't; epub.js rebuilds
          // the iframe per chapter).
          rendition.themes.default({
            body: {
              "font-family": fontFamily || "Literata Variable, Georgia, serif",
              "font-size": fontSize ? `${fontSize}px` : undefined,
              "line-height": lineHeight ? String(lineHeight) : undefined,
              color: "var(--color-ink-1)",
            },
          })

          // Fire a selection event up the tree whenever the user drags
          // across text inside the iframe. epub.js emits cfiRange + the
          // iframe's `contents` so we can pull the raw string from
          // window.getSelection().
          rendition.on("selected", (cfiRange: string, contents: any) => {
            let text = ""
            try {
              const sel = contents?.window?.getSelection?.()
              text = (sel?.toString() ?? "").trim()
            } catch {
              text = ""
            }
            if (!text) return
            onSelect?.({ cfiRange, text })
          })

          rendition.on("relocated", (location: any) => {
            if (!location?.start) return
            const cfi: string = location.start.cfi ?? ""
            const pct =
              typeof location.start.percentage === "number"
                ? location.start.percentage
                : book.locations?.percentageFromCfi
                  ? book.locations.percentageFromCfi(cfi)
                  : 0
            onProgress?.({ cfi, percent: pct })
          })

          await book.ready
          // Generate per-page locations so percentage_from_cfi works. The
          // 1024-char-per-slice default is the epub.js canonical density —
          // about one page per slice on modern screens.
          book.locations.generate(1024).catch(() => undefined)

          await rendition.display(initialCfi || undefined)
          // TS narrows flag.cancelled to false after the earlier guard,
          // even across `await` — but the cleanup mutates it at any
          // await resumption point, so the check still fires at runtime.
          // The condition is not provably unnecessary at runtime — the value comes
          // from an untyped library surface.
          if (flag.cancelled) return

          const toc: Array<EpubTocEntry> =
            book.navigation?.toc?.map(mapToc) ?? []
          onReady?.({ toc, totalPages: book.locations?.total ?? 0 })
          setBooted(true)
        } catch (err) {
          if (!flag.cancelled) onError?.(err)
        }
      })()

      return () => {
        flag.cancelled = true
        try {
          rendition?.destroy()
          book?.destroy()
        } catch {
          // epub.js occasionally throws during teardown on rapid re-mounts;
          // safe to swallow — the container is being detached anyway.
        }
        renditionRef.current = null
      }
      // Intentionally boot once per URL; typography changes flow through
      // the separate effect below so we don't tear down the rendition for
      // a font swap.
    }, [url, initialCfi])

    // Live typography — re-apply on change without remounting the book.
    useEffect(() => {
      const r = renditionRef.current
      if (!r || !booted) return
      r.themes.default({
        body: {
          "font-family": fontFamily || "Literata Variable, Georgia, serif",
          "font-size": fontSize ? `${fontSize}px` : undefined,
          "line-height": lineHeight ? String(lineHeight) : undefined,
        },
      })
    }, [fontFamily, fontSize, lineHeight, booted])

    // Keep the rendition's highlight overlay in sync with the `highlights`
    // prop. epub.js doesn't expose a bulk setter, so the naive approach —
    // remove everything, re-add — is what we do. At the volumes we care
    // about (dozens per book) this is imperceptible.
    useEffect(() => {
      const r = renditionRef.current
      if (!r || !booted) return
      const applied = new Set<string>()
      const list = highlights ?? []
      for (const h of list) {
        try {
          r.annotations.add("highlight", h.cfiRange, {}, undefined, "hl", {
            fill: h.color ?? "oklch(0.92 0.07 85)",
            "fill-opacity": "0.5",
            "mix-blend-mode": "multiply",
          })
          applied.add(h.cfiRange)
        } catch {
          // Invalid / stale CFI — skip; upstream UI still lists the note.
        }
      }
      return () => {
        for (const cfi of applied) {
          try {
            r.annotations.remove(cfi, "highlight")
          } catch {
            // The rendition may already be torn down; ignore.
          }
        }
      }
    }, [highlights, booted])

    useImperativeHandle(
      ref,
      () => ({
        next: () => renditionRef.current?.next?.(),
        prev: () => renditionRef.current?.prev?.(),
        goTo: (href: string) => renditionRef.current?.display?.(href),
      }),
      []
    )

    return (
      <div
        ref={containerRef}
        style={{
          width: "100%",
          height: "100%",
          background: "var(--color-paper-0)",
        }}
      />
    )
  }
)

function mapToc(node: any): EpubTocEntry {
  return {
    label: String(node.label ?? "").trim(),
    href: String(node.href ?? ""),
    subitems: Array.isArray(node.subitems)
      ? node.subitems.map(mapToc)
      : undefined,
  }
}
