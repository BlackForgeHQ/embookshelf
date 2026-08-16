import type { ReactNode } from "react"

import { PanelHeader, PanelLoading } from "@/components/SettingsShared"
import { Button } from "@/components/ui/button"

// Panel is the settings-panel frame, stated once (#354). The deep half
// of the settings tier — useSettingsDraft — was never restated; the
// frame around it was: the loading block seven times, the save row six,
// the header in three dialects (one panel shadowed the shared
// PanelHeader with a local one). A panel declares title, intro and its
// loading fact; the frame is the scaffold's.
export function Panel({
  title,
  intro,
  actions,
  loading = false,
  children,
}: {
  title: string
  intro?: ReactNode
  actions?: ReactNode
  loading?: boolean
  children: ReactNode
}) {
  return (
    <>
      <PanelHeader title={title} actions={actions}>
        {intro}
      </PanelHeader>
      {loading ? <PanelLoading /> : children}
    </>
  )
}

// SaveRow is the one save/dirty footer — and the first surface to offer
// Revert, which useSettingsDraft has implemented since it was written
// and no panel exposed (#354). Sits inside the panel's own <form>; the
// save button stays type="submit" so the form's onSubmit keeps owning
// the payload.
export function SaveRow({
  draft,
  label = "Save",
  disabled = false,
  onSave,
  align = "end",
  children,
}: {
  draft: { saving: boolean; dirty: boolean; revert: () => void }
  label?: string
  /** Extra refusal beyond saving — a field that fails local validation. */
  disabled?: boolean
  /** Click-based save for rows outside a <form>; omitted = type="submit". */
  onSave?: () => void
  align?: "start" | "end"
  /** Row content rendered beside the buttons (a test-connection button). */
  children?: ReactNode
}) {
  return (
    <div
      className={`mt-2 flex items-center gap-2 ${align === "end" ? "justify-end" : ""}`}
    >
      {children}
      {draft.dirty && !draft.saving && (
        <Button type="button" variant="ghost" size="sm" onClick={draft.revert}>
          Revert
        </Button>
      )}
      <Button
        type={onSave ? "button" : "submit"}
        size="sm"
        disabled={draft.saving || disabled}
        onClick={onSave}
      >
        {draft.saving ? "Saving…" : label}
      </Button>
    </div>
  )
}
