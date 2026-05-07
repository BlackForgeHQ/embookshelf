import { useMutation, useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "@tanstack/react-router"

import { meQueryKey, signup } from "@/api/auth"

// useSignup wraps the signup API call with the standard side effects:
// write the returned user into the cached `me` query and redirect to
// `target`. Pairs with useLogin / useLogout.
export function useSignup(target: string) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: (input: { email: string; name: string; password: string }) =>
      signup(input.email, input.name, input.password),
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user)
      void navigate({ to: target, replace: true })
    },
  })
}
