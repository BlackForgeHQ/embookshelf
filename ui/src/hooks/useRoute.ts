import { useMemo } from "react"
import { usePathname, useSearchParams } from "next/navigation"

// The current route's inputs. The parsers and builders they pair with are
// in src/lib/routes.ts, which stays hook-free so server layouts can import
// it.

/**
 * The `[id]` segment of the current URL.
 *
 * Not useParams: in the static export that returns the placeholder the
 * page was prerendered for, while the pathname is the real URL. Every
 * dynamic route in the app has the id as its second segment
 * (`/book/:id`, `/book/:id/edit`, `/read/:id`).
 */
export function useRouteId(): string {
  const pathname = usePathname()
  return useMemo(() => {
    const raw = pathname.split("/")[2] ?? ""
    try {
      return decodeURIComponent(raw)
    } catch {
      return raw
    }
  }, [pathname])
}

/** Parses the current query string with one of the parsers in src/lib/routes.ts. */
export function useRouteSearch<T>(parse: (params: URLSearchParams) => T): T {
  const params = useSearchParams()
  // ReadonlyURLSearchParams keeps one identity per navigation, so this
  // re-parses only when the query string actually changes.
  // biome-ignore lint/correctness/useExhaustiveDependencies: parse is a module-level function
  return useMemo(() => parse(new URLSearchParams(params.toString())), [params])
}
