export type ApiError = { status: number; message: string }

// Centralized fetch wrapper. Keeps credentials on so the session cookie
// travels with every request, and routes JSON errors through a typed shape.
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: {
      Accept: "application/json",
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      ...(init.headers ?? {}),
    },
    ...init,
  })

  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) message = body.error
    } catch {
      // non-JSON error body — keep statusText
    }
    throw { status: res.status, message } satisfies ApiError
  }

  // Handle every no-body success: 204/205, plus 202 Accepted from
  // fire-and-forget endpoints like POST /settings/libraries/paths/:id/scan
  // which return an empty body. Reading text first and only parsing when
  // it's non-empty avoids "Unexpected end of JSON input" on those.
  if (res.status === 204 || res.status === 205) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}
