import { Suspense } from "react"
import type { Metadata, Viewport } from "next"
import type { ReactNode } from "react"

import "../styles.css"
import { Providers } from "./providers"

export const metadata: Metadata = {
  title: "embookshelf",
}

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
}

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Providers>
          {/* Every page reads its query string with useSearchParams, which
              the static export can only resolve in the browser. One
              boundary here lets each page render client-side without
              repeating it. */}
          <Suspense>{children}</Suspense>
        </Providers>
      </body>
    </html>
  )
}
