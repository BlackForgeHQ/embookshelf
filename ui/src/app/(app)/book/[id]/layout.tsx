import type { ReactNode } from "react"

import { placeholderParams } from "@/lib/routes"

// Prerenders /book/_ (and its /edit and /find children) as the one shell
// every book id resolves to. See PLACEHOLDER_ID in src/lib/routes.ts.
export const generateStaticParams = placeholderParams

export default function BookLayout({ children }: { children: ReactNode }) {
  return children
}
