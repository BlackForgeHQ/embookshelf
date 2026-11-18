import { useMutation, useQueryClient } from "@tanstack/react-query"

import { logout as apiLogout, meQueryKey } from "@/api/auth"
import { paths } from "@/lib/routes"
import { useAppRouter } from "@/lib/navigation"

/**
 * useLogout wraps the logout API call with the standard side effects:
 * clear the cached `me` query and redirect.
 *
 * When forward-auth is enabled and the server returns a `logoutUrl`,
 * the browser is redirected to the upstream proxy's logout endpoint
 * (Authelia /logout, etc.) so the proxy session is killed too —
 * otherwise the next request would re-attach the user via headers.
 * ADR-0022.
 */
export function useLogout() {
  const queryClient = useQueryClient()
  const router = useAppRouter()
  return useMutation({
    mutationFn: apiLogout,
    onSuccess: (result) => {
      queryClient.setQueryData(meQueryKey, null)
      if (result.logoutUrl) {
        window.location.href = result.logoutUrl
        return
      }
      router.replace(paths.login())
    },
  })
}
