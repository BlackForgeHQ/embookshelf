import { useEffect, useMemo, useRef } from "react"
import { useRouter } from "next/navigation"

// Next.js has no navigation blocker, and the metadata editor needs one: a
// dirty form must not be dropped by a sidebar click, a command-palette
// jump or the back button. A page registers a guard with
// useNavigationGuard; every in-app way of leaving consults the registered
// guards first:
//
//   - <Link>/<a> clicks — intercepted at the document before Next sees them
//   - router.push / router.replace — through useAppRouter below
//   - back/forward — through the Navigation API, where the browser has it
//
// A full unload (tab close, reload, typed URL) is the page's own
// beforeunload handler: browsers only allow their own prompt there.

/** Returns true to block the navigation. May ask the user first. */
type Guard = () => boolean

const guards = new Set<Guard>()

/** Whether any registered guard refuses the navigation about to happen. */
export function navigationBlocked(): boolean {
  for (const guard of guards) {
    if (guard()) return true
  }
  return false
}

/**
 * The app's router: Next's, with push and replace refused while a
 * registered guard says so. Use it instead of next/navigation's useRouter
 * so programmatic navigation cannot skip an unsaved-changes prompt.
 */
export function useAppRouter() {
  const router = useRouter()
  return useMemo(
    () => ({
      ...router,
      push: (href: string, options?: Parameters<typeof router.push>[1]) => {
        if (navigationBlocked()) return
        router.push(href, options)
      },
      replace: (
        href: string,
        options?: Parameters<typeof router.replace>[1]
      ) => {
        if (navigationBlocked()) return
        router.replace(href, options)
      },
    }),
    [router]
  )
}

type NavigateEventLike = Event & {
  navigationType: "push" | "replace" | "reload" | "traverse"
  cancelable: boolean
}

// Capture phase on the document runs before React's root listener, so a
// refused click never reaches <Link>'s own handler.
function onClick(e: MouseEvent) {
  if (e.defaultPrevented || e.button !== 0) return
  if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
  const anchor = (e.target as Element | null)?.closest?.("a[href]")
  if (!(anchor instanceof HTMLAnchorElement)) return
  if (anchor.target && anchor.target !== "_self") return
  if (anchor.hasAttribute("download")) return
  const url = new URL(anchor.href, window.location.href)
  if (url.origin !== window.location.origin) return
  if (
    url.pathname === window.location.pathname &&
    url.search === window.location.search
  )
    return
  if (navigationBlocked()) {
    e.preventDefault()
    e.stopPropagation()
  }
}

// Back/forward. Progressive: without the Navigation API the guards still
// cover clicks and router calls, just not the browser buttons.
function onNavigate(e: Event) {
  const event = e as NavigateEventLike
  if (event.navigationType !== "traverse" || !event.cancelable) return
  if (navigationBlocked()) event.preventDefault()
}

function navigationApi(): EventTarget | undefined {
  return (window as unknown as { navigation?: EventTarget }).navigation
}

/**
 * Registers `shouldBlock` for as long as the calling component is
 * mounted. Read through a ref, so the caller can pass a fresh closure
 * every render without re-registering.
 */
export function useNavigationGuard(shouldBlock: Guard) {
  const ref = useRef(shouldBlock)
  ref.current = shouldBlock

  useEffect(() => {
    const guard: Guard = () => ref.current()
    // The listeners are shared by every guard, so one click asks once.
    if (guards.size === 0) {
      document.addEventListener("click", onClick, true)
      navigationApi()?.addEventListener("navigate", onNavigate)
    }
    guards.add(guard)
    return () => {
      guards.delete(guard)
      if (guards.size === 0) {
        document.removeEventListener("click", onClick, true)
        navigationApi()?.removeEventListener("navigate", onNavigate)
      }
    }
  }, [])
}
