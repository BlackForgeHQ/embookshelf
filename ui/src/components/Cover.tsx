import { useState } from "react"
import { Icon } from "./Icon"
import type { CSSProperties, MouseEventHandler } from "react"

import { bookCoverUrl } from "@/api/books"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export type CoverPalette =
  | "navy"
  | "olive"
  | "rust"
  | "teal"
  | "plum"
  | "ochre"
  | "forest"
  | "cream"
  | "brick"
  | "ink"

export type CoverStyle =
  | "centered-line"
  | "minimal-top"
  | "stacked-serif"
  | "block"
  | "typographic"

// CoverBook is the minimal shape Cover needs to render. Accepting a
// permissive string for palette + style lets the same component render
// both the mock-data prototype and live API books without casts — unknown
// values just fall back to sensible defaults.
export type CoverBook = {
  title: string
  author: string
  palette?: string
  style?: string
  placeholder?: boolean
  // Format string like "epub" / "pdf" / "cbz". When set, Cover renders a
  // small colored pill in the top-left corner (see FormatBadge below).
  format?: string
  // id + hasCover drive the image-vs-typographic choice. Both must be
  // present for <img> rendering — id for the URL, hasCover to tell the
  // component the backend has extracted bytes. On image-load failure we
  // fall back to the typographic tile.
  id?: string
  hasCover?: boolean
}

type CoverSize = "xs" | "sm" | "md" | "lg" | "hero"

const PALETTES: Record<CoverPalette, { bg: string; ink: string }> = {
  navy: { bg: "var(--color-cov-navy)", ink: "oklch(0.88 0.04 85)" },
  olive: { bg: "var(--color-cov-olive)", ink: "oklch(0.92 0.03 80)" },
  rust: { bg: "var(--color-cov-rust)", ink: "oklch(0.92 0.04 85)" },
  teal: { bg: "var(--color-cov-teal)", ink: "oklch(0.90 0.03 180)" },
  plum: { bg: "var(--color-cov-plum)", ink: "oklch(0.90 0.03 60)" },
  ochre: { bg: "var(--color-cov-ochre)", ink: "oklch(0.18 0.02 60)" },
  forest: { bg: "var(--color-cov-forest)", ink: "oklch(0.90 0.03 85)" },
  cream: { bg: "var(--color-cov-cream)", ink: "oklch(0.24 0.02 60)" },
  brick: { bg: "var(--color-cov-brick)", ink: "oklch(0.90 0.04 85)" },
  ink: { bg: "var(--color-cov-ink)", ink: "oklch(0.90 0.03 85)" },
}

const VALID_STYLES: ReadonlyArray<CoverStyle> = [
  "centered-line",
  "minimal-top",
  "stacked-serif",
  "block",
  "typographic",
]

function coerceStyle(raw: string | undefined): CoverStyle {
  if (raw && (VALID_STYLES as ReadonlyArray<string>).includes(raw)) {
    return raw as CoverStyle
  }
  return "centered-line"
}

function CoverInner({ book, size }: { book: CoverBook; size: CoverSize }) {
  if (book.placeholder) {
    return (
      <>
        <div className="c-top">
          <div className="c-author mono">{book.author}</div>
        </div>
        <div className="c-title">{book.title}</div>
      </>
    )
  }
  const style = coerceStyle(book.style)
  switch (style) {
    case "centered-line":
      return (
        <>
          <div className="c-top">
            <div className="c-author">{book.author.toUpperCase()}</div>
            <div className="c-ornament" />
          </div>
          <div className="c-title" style={{ textAlign: "left" }}>
            {book.title}
          </div>
        </>
      )
    case "minimal-top":
      return (
        <>
          <div className="c-title" style={{ fontStyle: "italic" }}>
            {book.title}
          </div>
          <div className="c-author">{book.author}</div>
        </>
      )
    case "stacked-serif":
      return (
        <>
          <div className="grow" />
          <div>
            <div
              className="c-title"
              style={{
                fontFamily: "var(--font-serif)",
                fontWeight: 600,
                letterSpacing: "-0.01em",
              }}
            >
              {book.title}
            </div>
            <div
              className="c-ornament"
              style={{ margin: "8px 0", width: "20%" }}
            />
            <div className="c-author">{book.author}</div>
          </div>
        </>
      )
    case "block": {
      const blockFontSize =
        size === "hero" ? 30 : size === "lg" ? 22 : undefined
      return (
        <>
          <div className="c-author">{book.author}</div>
          <div
            className="c-title"
            style={{
              fontSize: blockFontSize,
              fontWeight: 700,
              textTransform: "uppercase",
              lineHeight: 1,
              letterSpacing: "-0.01em",
            }}
          >
            {book.title}
          </div>
        </>
      )
    }
    case "typographic": {
      const typoSize = size === "hero" ? 32 : size === "lg" ? 24 : undefined
      return (
        <>
          <div
            className="c-top"
            style={{
              flex: 1,
              justifyContent: "center",
              alignItems: "center",
              display: "flex",
            }}
          >
            <div
              className="c-title"
              style={{
                fontFamily: "var(--font-serif)",
                fontWeight: 300,
                fontStyle: "italic",
                textAlign: "center",
                fontSize: typoSize,
              }}
            >
              {book.title}
            </div>
          </div>
          <div className="c-author" style={{ textAlign: "center" }}>
            {book.author}
          </div>
        </>
      )
    }
    default:
      return null
  }
}

type CoverProps = {
  book: CoverBook
  size?: CoverSize
  onClick?: MouseEventHandler<HTMLDivElement>
  style?: CSSProperties
}

function coercePalette(raw: string | undefined): CoverPalette {
  if (raw && raw in PALETTES) return raw as CoverPalette
  return "navy"
}

export function Cover({ book, size = "md", onClick, style }: CoverProps) {
  const palette = PALETTES[coercePalette(book.palette)]
  const isPlaceholder = Boolean(book.placeholder)
  const format = normalizeFormat(book.format)

  // Render the extracted image when the backend has one. A bad/missing
  // file on disk returns 404 from /books/:id/cover; the <img>'s onError
  // flips this flag and we degrade to the typographic tile without a
  // broken-image glyph.
  const [imgFailed, setImgFailed] = useState(false)
  const showImage =
    !isPlaceholder && Boolean(book.id) && Boolean(book.hasCover) && !imgFailed

  const baseStyle: CSSProperties =
    isPlaceholder || showImage
      ? {}
      : { background: palette.bg, color: palette.ink }

  return (
    <div
      className={`cover ${size} ${isPlaceholder ? "placeholder" : ""}`}
      style={{
        position: "relative",
        overflow: "hidden",
        ...baseStyle,
        ...style,
      }}
      onClick={onClick}
    >
      {showImage ? (
        <img
          src={bookCoverUrl(book.id as string)}
          alt={book.title}
          loading="lazy"
          onError={() => setImgFailed(true)}
          style={{
            position: "absolute",
            inset: 0,
            width: "100%",
            height: "100%",
            objectFit: "cover",
            display: "block",
          }}
        />
      ) : (
        <CoverInner book={book} size={size} />
      )}
      {format && <FormatBadge format={format} size={size} />}
    </div>
  )
}

// normalizeFormat returns the canonical uppercase label ("EPUB", "PDF",
// "CBZ", …) or null when the book has no format string — e.g. mock
// data for covers that aren't backed by a file.
function normalizeFormat(raw: string | undefined): string | null {
  if (!raw) return null
  const trimmed = raw.trim().replace(/^\./, "").toUpperCase()
  return trimmed === "" ? null : trimmed
}

// Color map is explicit per format so the badge is recognizable at a
// glance. Unknown formats fall through to the neutral secondary
// variant — still visible, just not color-coded.
const FORMAT_CLASSES: Record<string, string> = {
  EPUB: "bg-emerald-600 text-white border-transparent",
  PDF: "bg-red-600 text-white border-transparent",
  CBZ: "bg-indigo-600 text-white border-transparent",
  CBR: "bg-indigo-600 text-white border-transparent",
  MOBI: "bg-amber-600 text-white border-transparent",
  AZW3: "bg-amber-600 text-white border-transparent",
  FB2: "bg-sky-600 text-white border-transparent",
  TXT: "bg-slate-600 text-white border-transparent",
}

function FormatBadge({ format, size }: { format: string; size: CoverSize }) {
  const cls = FORMAT_CLASSES[format]
  // Smaller sizes get a tighter pill so it doesn't crowd the art.
  const compact = size === "xs" || size === "sm"
  return (
    <Badge
      aria-label={`format ${format}`}
      className={cn(
        "absolute top-1.5 left-1.5 tracking-wide uppercase",
        compact ? "h-4 px-1.5 text-[9px]" : "h-5 px-2 text-[10px]",
        cls
      )}
      variant={cls ? "default" : "secondary"}
    >
      {format}
    </Badge>
  )
}

const SPINE_WIDTHS = [26, 32, 22, 38, 28, 34, 24, 30]
const SPINE_HEIGHTS = [210, 230, 200, 220, 240, 215, 205, 225]

type SpineProps = {
  book: CoverBook
  index?: number
  onClick?: MouseEventHandler<HTMLDivElement>
}

export function Spine({ book, index = 0, onClick }: SpineProps) {
  const palette = PALETTES[coercePalette(book.palette)]
  const isPlaceholder = Boolean(book.placeholder)
  const w = SPINE_WIDTHS[index % SPINE_WIDTHS.length]
  const h = SPINE_HEIGHTS[index % SPINE_HEIGHTS.length]
  return (
    <div
      onClick={onClick}
      style={{
        width: w,
        height: h,
        background: isPlaceholder ? "var(--color-paper-3)" : palette.bg,
        color: isPlaceholder ? "var(--color-ink-2)" : palette.ink,
        display: "flex",
        alignItems: "flex-end",
        justifyContent: "center",
        padding: "12px 4px",
        cursor: "pointer",
        position: "relative",
        boxShadow:
          "inset 1px 0 0 oklch(0 0 0 / 0.2), inset -1px 0 0 oklch(0 0 0 / 0.15), 1px 2px 4px oklch(0.2 0.02 60 / 0.15)",
        flexShrink: 0,
        transition: "transform 180ms ease",
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.transform = "translateY(-6px)"
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.transform = ""
      }}
    >
      <div
        style={{
          writingMode: "vertical-rl",
          transform: "rotate(180deg)",
          fontSize: 10,
          fontFamily: "var(--font-serif)",
          fontWeight: 500,
          letterSpacing: "0.02em",
          textOverflow: "ellipsis",
          overflow: "hidden",
          whiteSpace: "nowrap",
          maxHeight: h - 24,
        }}
      >
        {book.title}
      </div>
    </div>
  )
}

type StarRatingProps = {
  rating: number
  size?: number
}

export function StarRating({ rating, size = 13 }: StarRatingProps) {
  const full = Math.floor(rating)
  const fractional = rating - full
  const half = fractional >= 0.3 && fractional <= 0.7
  return (
    <div style={{ display: "flex", gap: 1, color: "var(--color-accent)" }}>
      {[0, 1, 2, 3, 4].map((i) => {
        const name =
          i < full ? "star-filled" : i === full && half ? "star-half" : "star"
        return <Icon key={i} name={name} size={size} />
      })}
    </div>
  )
}
