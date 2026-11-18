import type { ReactNode } from "react"

import { placeholderParams } from "@/lib/routes"

// Prerenders /read/_ as the one shell every book id resolves to. See
// PLACEHOLDER_ID in src/lib/routes.ts.
export const generateStaticParams = placeholderParams

export default function ReaderLayout({ children }: { children: ReactNode }) {
  return children
}
