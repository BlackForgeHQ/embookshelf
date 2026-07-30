import type { ReactNode } from "react"

type Props = {
  label: string
  value: ReactNode
  /**
   * Label column width in px. The settings Cards run 160 against a
   * 640px panel; the book overview runs 96 because its values are
   * prose and the label should not eat a quarter of the column.
   */
  labelWidth?: number
  /**
   * A dashed hairline under the row. The book overview reads as a
   * ruled ledger; the settings Cards already have a border of their
   * own and a second one inside them reads as clutter.
   */
  rule?: boolean
}

/**
 * The one label-left / value-right row.
 *
 * There were three: this one in SettingsShared, a `Meta` in the book
 * overview spelling the same flex row out with inline styles, and the
 * two differed on the label width, the gap and the vertical padding by
 * a pixel or two each — differences nobody chose (ADR-0029).
 *
 * Not the BookDrop detail's `MetaCell`, which looks like a fourth
 * spelling and is not one: its label sits *above* its value, it is a
 * cell in a two-column grid rather than a row in a stack, and it owns
 * the dimmed "could not detect" fallback that only that pane needs.
 */
export function DefRow({ label, value, labelWidth = 160, rule }: Props) {
  return (
    <div
      className={`flex items-baseline gap-3 py-1.5${
        rule ? " border-b border-dashed border-(--color-rule-soft)" : ""
      }`}
    >
      <div className="t-label shrink-0" style={{ width: labelWidth }}>
        {label}
      </div>
      <div className="min-w-0 flex-1 text-[13.5px] break-words">{value}</div>
    </div>
  )
}
