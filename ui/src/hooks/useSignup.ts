import { useMutation, useQueryClient } from "@tanstack/react-query"

import { meQueryKey, signup } from "@/api/auth"
import { useAppRouter } from "@/lib/navigation"

// useSignup wraps the signup API call with the standard side effects:
// write the returned user into the cached `me` query and redirect to
// `target`. Pairs with useLogin / useLogout.
export function useSignup(target: string) {
  const queryClient = useQueryClient()
  const router = useAppRouter()
  return useMutation({
    mutationFn: (input: { email: string; name: string; password: string }) =>
      signup(input.email, input.name, input.password),
    onSuccess: (user) => {
      queryClient.setQueryData(meQueryKey, user)
      router.replace(target)
    },
  })
}
