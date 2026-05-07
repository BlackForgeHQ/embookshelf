import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { login, meQueryKey } from "@/api/auth"

// useLogin wraps the login API call with the standard side effects:
// write the returned user into the cached `me` query and redirect to
// `target`. Pairs with useSignup and useLogout so every auth-state
// transition flows through one shape.
export function useLogin(target: string) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: (creds: { email: string; password: string }) =>
      login(creds.email, creds.password),
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user)
      void navigate({ to: target, replace: true })
    },
  })
}
