import type { ReactNode } from "react"
import { toast } from "sonner"

import type { Locator } from "@/lib/locator"
import { Icon } from "@/components/Icon"
import { Button } from "@/components/ui/button"

/**
 * The chrome the three reader shells share (ADR-0029 §2, slice 2).
 *
 * The split this file draws is lifecycle: the frame around a reader —
 * container, header strip, exit, bookmark, footer, chrome-restore — was
 * spelled two or three times, character-identical but for a background
 * token and the locator a bookmark points at. None of it holds state, a
 * ref or a query, which is exactly why it can be shared when renderer
 * props, imperative handles and restore timing cannot.
 *
 * Nothing here decides *whether* it renders. The shells disagree on that
 * — two gate the header on `chromeVisible` and audio has no such state —
 * and folding those differences in would put the shells' control flow
 * somewhere they cannot see it.
 */

type ReaderContainerProps = {
  /**
   * The page under the reader. The one thing that varies between the
   * three shells: paper-0 for text, paper-2 for comics, paper-1 for
   * audio.
   */
  background: string
  children: ReactNode
}

/**
 * The fullscreen surface a reader lives in. Fixed and above the app
 * chrome, because a reader replaces the page rather than sitting in it.
 */
export function ReaderContainer({
  background,
  children,
}: ReaderContainerProps) {
  return (
    <div
      className="fade-in"
      style={{
        position: "fixed",
        inset: 0,
        background,
        zIndex: 200,
        display: "flex",
        flexDirection: "column",
      }}
    >
      {children}
    </div>
  )
}

/**
 * The top strip. What sits in it is per-shell — a TOC and type toggle for
 * text, a fit-mode group for comics, a chapter drawer for audio — but the
 * strip itself is the same bar in all three.
 */
export function ReaderHeader({ children }: { children: ReactNode }) {
  return (
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
      {children}
    </div>
  )
}

/**
 * Leaves the reader. Every shell flushes its reading position before
 * navigating rather than relying on the unmount backstop, so the handler
 * stays with the shell that owns the position.
 */
export function ExitButton({ onExit }: { onExit: () => void }) {
  return (
    <Button variant="ghost" size="sm" onClick={onExit}>
      <Icon name="arrow-left" size={14} /> Library
    </Button>
  )
}

type BookmarkButtonProps = {
  /**
   * Where the bookmark points. Each shell names a position in its own
   * currency — a CFI, a page, a time — so the button takes the finished
   * locator rather than trying to derive one.
   *
   * Null means the surface has no position yet. Only the text shell can
   * be in that state: it is mounted before the reader reports its first
   * progress event, and until then there is nothing to bookmark.
   */
  locator: Locator | null
  /** True while a bookmark write is in flight. */
  pending: boolean
  onBookmark: (locator: Locator) => void
}

export function BookmarkButton({
  locator,
  pending,
  onBookmark,
}: BookmarkButtonProps) {
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Bookmark"
      disabled={pending}
      onClick={() => {
        if (!locator) {
          toast.info("Open the book first, then bookmark.")
          return
        }
        onBookmark(locator)
      }}
    >
      <Icon name="bookmark" size={14} />
    </Button>
  )
}

type ReaderFooterProps = {
  onPrev: () => void
  onNext: () => void
  /** Where the reader is now — `p.7`, `43%`, or `—` when unknown. */
  leftLabel: string
  /** Where it ends — `p.240`, or the percentage when there is no total. */
  rightLabel: string
  /**
   * The progress bar, between the two labels. It arrives as children
   * because the shells label and seek it differently, and the bar is
   * already its own component (slice 1) — the footer only owns the row.
   */
  children: ReactNode
}

/**
 * The page-turn row: prev, position, bar, total, next.
 *
 * Only the paged shells have one; audio's transport is a different
 * control topology and lives in its own layout.
 */
export function ReaderFooter({
  onPrev,
  onNext,
  leftLabel,
  rightLabel,
  children,
}: ReaderFooterProps) {
  return (
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
      <Button variant="ghost" size="sm" onClick={onPrev}>
        <Icon name="chevron-left" size={14} /> Prev
      </Button>
      <div style={{ flex: 1, display: "flex", alignItems: "center", gap: 12 }}>
        <span
          className="mono"
          style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
        >
          {leftLabel}
        </span>
        {children}
        <span
          className="mono"
          style={{ fontSize: 10.5, color: "var(--color-ink-3)" }}
        >
          {rightLabel}
        </span>
      </div>
      <Button variant="ghost" size="sm" onClick={onNext}>
        Next <Icon name="chevron-right" size={14} />
      </Button>
    </div>
  )
}

/**
 * Brings the header back once chrome is hidden.
 *
 * A fallback, not the primary gesture: the shells also restore from the
 * reading area itself, and they disagree on how — the text shell takes a
 * single click (an EPUB iframe swallows it, so only the letterbox
 * margins respond), the comic shell a double-click, because a single one
 * is already a page turn. That disagreement is why the gesture stays at
 * the call site and only the button is shared.
 */
export function ChromeRestoreButton({ onRestore }: { onRestore: () => void }) {
  return (
    <Button
      variant="outline"
      size="icon-sm"
      onClick={onRestore}
      aria-label="Show reader controls"
      className="absolute top-2 right-2 z-10"
    >
      <Icon name="menu" size={14} />
    </Button>
  )
}
