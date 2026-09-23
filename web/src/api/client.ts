// Minimal JSON fetch wrapper around the gateway's /api endpoints. Session
// auth is cookie-based, so requests just need credentials: "include" (the
// GUI is same-origin in both dev and prod). A 401 anywhere bounces to login.

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

let onUnauthorized: (() => void) | null = null

/** Called once from App on mount: 401s redirect to the login route. */
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: init?.body ? { "Content-Type": "application/json" } : undefined,
    ...init,
  })
  if (res.status === 401 && onUnauthorized) {
    onUnauthorized()
    throw new ApiError(401, "login required")
  }
  let body: unknown = null
  try {
    body = await res.json()
  } catch {
    // empty body is fine
  }
  if (!res.ok) {
    const msg =
      (body as { error?: { message?: string } } | null)?.error?.message ??
      `request failed (${res.status})`
    throw new ApiError(res.status, msg)
  }
  return body as T
}

export const post = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) })

export const put = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PUT", body: JSON.stringify(body) })

export const patch = <T>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) })

export const del = <T>(path: string) => api<T>(path, { method: "DELETE" })
