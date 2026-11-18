import { useEffect, useRef, useState } from "react"

import { Button } from "@/components/ui/button"

// CopyButton copies a value and says so for a moment. The clipboard API
// is absent outside a secure context (plain-HTTP self-hosted installs),
// so a failure says "Copy failed" rather than claiming success.
export function CopyButton({
  value,
  label = "Copy",
  size = "sm",
}: {
  value: string
  label?: string
  size?: "sm" | "default"
}) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle")
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearTimeout(timer.current), [])

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setState("copied")
    } catch {
      setState("failed")
    }
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setState("idle"), 1500)
  }

  return (
    <Button type="button" variant="outline" size={size} onClick={copy}>
      {state === "copied" ? "Copied" : state === "failed" ? "Copy failed" : label}
    </Button>
  )
}
