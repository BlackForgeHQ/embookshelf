// Machine-readable error codes the server may attach. Branch on these
// rather than matching message text — messages are for display and are
// free to change. Mirrors the constants in internal/handler/errjson.go.
export type ApiErrorCode =
  | "EMAIL_DISABLED"
  | "KINDLE_EMAIL_UNSET"
  | "FORMAT_NOT_SUPPORTED"
  | "EMAIL_RELOAD_FAILED"
  | "SMTP_ERROR"

// The server's error envelope is flat: `error` is always the display
// message, `code` is present only when the case is one a client is
// expected to handle specially.
export type ApiError = { status: number; message: string; code?: ApiErrorCode }

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
    let code: ApiErrorCode | undefined
    try {
      const body = (await res.json()) as { error?: string; code?: ApiErrorCode }
      // Guard the type rather than trusting it: an older server, or a
      // handler that bypasses the envelope, could still send a non-string
      // here, and assigning that into `message` is what used to render as
      // "[object Object]".
      if (typeof body.error === "string" && body.error) message = body.error
      if (typeof body.code === "string") code = body.code
    } catch {
      // non-JSON error body — keep statusText
    }
    throw { status: res.status, message, code } satisfies ApiError
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
