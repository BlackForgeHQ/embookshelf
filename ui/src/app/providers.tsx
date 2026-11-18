"use client"

import { useState } from "react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import type { ReactNode } from "react"

import { Toaster } from "@/components/ui/sonner"
import { setupTelemetry } from "@/telemetry"

// Initialize browser OTel as early as possible — document-load spans need
// the provider registered before the 'load' event fires to capture
// resource timings. setupTelemetry() is a no-op outside the browser, so
// the static prerender passes through it untouched.
setupTelemetry()

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { staleTime: 30_000, refetchOnWindowFocus: false },
        },
      })
  )
  return (
    <QueryClientProvider client={queryClient}>
      {children}
      <Toaster position="bottom-right" />
    </QueryClientProvider>
  )
}
