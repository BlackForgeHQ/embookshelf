import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { logout as apiLogout, meQueryKey } from "@/api/auth"

/**
 * useLogout wraps the logout API call with the standard side effects:
 * clear the cached `me` query and redirect to /login. Used by both the
 * sidebar user badge and the command palette so they stay in lock-step.
 */
export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: apiLogout,
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null)
      void navigate({ to: "/login", replace: true })
    },
  })
}
