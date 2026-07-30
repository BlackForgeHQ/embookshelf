import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

type Props = {
  /** The sentence to show. Markup is fine — one caller emphasises a count. */
  children: ReactNode
  /**
   * Spacing for the slot this sits in, and nothing else: `mb-6` in the
   * BookDrop detail pane, `mt-2.5` under the drop zone, `mx-5 my-2` in
   * the rail. The tokens are the component's.
   */
  className?: string
}

/**
 * The one inline error notice.
 *
 * There were twelve, in four idioms: `style={{ ... }}` spelling out the
 * accent tokens three times, Tailwind arbitrary values five times, the
 * `.flash error` class twice — with the class's own rule restated
 * inline beside it — and two bare paragraphs tinted `--color-accent-ink`.
 * Four of the twelve set `role="alert"`, so whether a failed save was
 * announced depended on which panel you were standing in (ADR-0029).
 *
 * The look is `.flash.error` from styles.css, which already described
 * exactly this box and which login.tsx applied *and* duplicated inline.
 * An e2e spec asserts on that selector; keeping it means the CSS has one
 * definition and one reader rather than being quietly orphaned.
 *
 * This is the inline report — the thing shown next to the control that
 * refused. Failures that should interrupt go to `toast.error` via
 * `useApiMutation`; the two are different, and the mutation hook already
 * knows which is which (`reportErrors: "inline"`).
 */
export function Notice({ children, className }: Props) {
  return (
    // Always an alert. Eight of the twelve blocks were not, and there is
    // no case where a message that only appears on failure should go
    // unannounced.
    <div role="alert" className={cn("flash error", className)}>
      {children}
    </div>
  )
}
