"use client"

import { useEffect, useState } from "react"
import type { ReactNode } from "react"

import { meQuery } from "@/api/auth"
import { useRealtime } from "@/api/realtime"
import { instanceSummaryQuery } from "@/api/settings"
import { useApiQuery } from "@/api/query"
import { CommandPalette } from "@/components/CommandPalette"
import { AppSidebar } from "@/components/Sidebar"
import { ShelfDraftProvider } from "@/components/ShelfDraftProvider"
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar"
import { TooltipProvider } from "@/components/ui/tooltip"
import { paths } from "@/lib/routes"
import { useAppRouter } from "@/lib/navigation"

// Route-group layout: every in-app screen (dashboard, library, book detail,
// bookdrop, settings, notebook, metadata editor) renders inside this shell
// with the sidebar on the left and the persistent status bar at the bottom.
// The reader is intentionally OUTSIDE this group — it's a full-screen
// takeover.
//
// The layout enforces authentication: nothing in the group renders until
// /me answers, and a signed-out answer redirects to /login with the
// current URL as `next`. The me query lives in the shared QueryClient, so
// the sidebar and every page reuse it without refetching.
export default function AuthedLayout({ children }: { children: ReactNode }) {
  const me = useApiQuery(meQuery)
  const router = useAppRouter()
  const signedOut = me.isSuccess && !me.data

  useEffect(() => {
    if (!signedOut) return
    const here = window.location.pathname + window.location.search
    router.replace(paths.login({ next: here }))
  }, [signedOut, router])

  if (me.isError) {
    return (
      <main className="container mx-auto p-4 pt-16">
        <p className="t-small">
          Could not reach the server.{" "}
          <button
            type="button"
            className="underline"
            onClick={() => void me.refetch()}
          >
            Retry
          </button>
        </p>
      </main>
    )
  }
  // The static prerender and the first client render both land here, so
  // the HTML the export ships and the tree React hydrates agree.
  if (!me.data) return null
  return <AppLayout>{children}</AppLayout>
}

function AppLayout({ children }: { children: ReactNode }) {
  // Only runs inside the authed shell — signed-out visitors are redirected
  // above and never mount this component, so the EventSource never fires
  // without a valid session cookie.
  useRealtime()

  const [paletteOpen, setPaletteOpen] = useState(false)

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault()
        setPaletteOpen((prev) => !prev)
      }
    }
    function onCustom() {
      setPaletteOpen(true)
    }
    window.addEventListener("keydown", onKey)
    window.addEventListener("embookshelf:open-command", onCustom)
    return () => {
      window.removeEventListener("keydown", onKey)
      window.removeEventListener("embookshelf:open-command", onCustom)
    }
  }, [])

  return (
    <TooltipProvider delayDuration={100}>
      <ShelfDraftProvider>
        <SidebarProvider className="h-screen overflow-hidden">
          <AppSidebar />
          <SidebarInset className="min-h-0 overflow-hidden">
            <div className="main-content">{children}</div>
            <StatusBar />
          </SidebarInset>
          <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
        </SidebarProvider>
      </ShelfDraftProvider>
    </TooltipProvider>
  )
}

function StatusBar() {
  // Counts come from /api/v1/instance alongside version+mode; the endpoint
  // is cheap (single COUNT(*) per library), and its spec says how long the
  // answer keeps.
  const instance = useApiQuery(instanceSummaryQuery)

  const version = instance.data?.version ?? "…"
  const libCount = instance.data?.libraries
  const bookCount = instance.data?.books

  const catalog =
    libCount != null && bookCount != null
      ? `${libCount} ${libCount === 1 ? "library" : "libraries"} · ${bookCount.toLocaleString()} ${bookCount === 1 ? "volume" : "volumes"}`
      : null

  return (
    <div className="status-bar">
      <span className="status-dot green" />
      <span>embookshelf {version}</span>
      {catalog && (
        <>
          <span>·</span>
          <span>{catalog}</span>
        </>
      )}
      <div className="grow" />
    </div>
  )
}
