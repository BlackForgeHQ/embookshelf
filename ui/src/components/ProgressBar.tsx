import type { CSSProperties, ReactNode } from "react"

type Props = {
  /** Progress as a 0–1 fraction. Out-of-range and non-finite are clamped. */
  value: number
  /** Accessible name for the filled portion. */
  label: string
  /**
   * Called with the clicked position as a 0–1 fraction. Passing it is
   * what makes the track seekable — the reader's comic and audio bars
   * are, its text bar and every bar outside the reader are not.
   */
  onSeek?: (fraction: number) => void
  /** Overlaid inside the track. The audio bar puts chapter ticks here. */
  children?: ReactNode
  style?: CSSProperties
}

/**
 * The one progress bar.
 *
 * There were ten, in three idioms: this markup three times in the reader
 * route, an inline `--color-rule-soft` variant twice, Tailwind arbitrary
 * values twice, and the `.progress` class three times — while
 * `.reader-progress` sat in styles.css, written for exactly this
 * component at exactly these dimensions, referenced by nothing
 * (ADR-0029, slice 1).
 *
 * Deliberately not the shadcn Progress primitive: two callers overlay
 * children inside the track and two seek on click, neither of which that
 * primitive exposes.
 */
export function ProgressBar({ value, label, onSeek, children, style }: Props) {
  const percent = Math.round(clampFraction(value) * 100)

  return (
    <div
      role={onSeek ? "slider" : "presentation"}
      aria-valuenow={onSeek ? percent : undefined}
      aria-valuemin={onSeek ? 0 : undefined}
      aria-valuemax={onSeek ? 100 : undefined}
      aria-label={onSeek ? label : undefined}
      tabIndex={onSeek ? 0 : undefined}
      onClick={
        onSeek &&
        ((e) => {
          const box = e.currentTarget.getBoundingClientRect()
          // A zero-width box is what jsdom reports and what a track
          // inside a collapsed flex parent reports; dividing by it would
          // hand the caller NaN and seek to the start of the book.
          if (box.width <= 0) return
          onSeek(clampFraction((e.clientX - box.left) / box.width))
        })
      }
      style={{
        flex: 1,
        position: "relative",
        height: 4,
        background: "var(--color-paper-3)",
        borderRadius: 2,
        cursor: onSeek ? "pointer" : "default",
        ...style,
      }}
    >
      <div
        aria-label={label}
        style={{
          height: 4,
          width: `${percent}%`,
          background: "var(--color-accent)",
          borderRadius: 2,
          transition: "width 120ms linear",
        }}
      />
      {children}
    </div>
  )
}

/**
 * 0–1, always.
 *
 * NaN reaches this from done-over-total where total is zero — a run
 * whose plan has not been written yet — and `width: NaN%` renders as a
 * full bar in some browsers, which reads as finished.
 */
function clampFraction(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(1, Math.max(0, value))
}
