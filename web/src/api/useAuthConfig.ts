import { useQuery } from "@tanstack/react-query"
import { api } from "./client"
import type { AuthConfigView } from "./types"

// useAuthConfig reads the server's sign-in methods (GET /api/auth/config,
// unauthenticated). One query key app-wide so a settings save invalidates
// every reader at once.
export function useAuthConfig() {
  return useQuery({
    queryKey: ["auth-config"],
    queryFn: () => api<AuthConfigView>("/api/auth/config"),
  })
}

// useCredentialLogin reports whether email + password sign-in is on.
// Unknown (loading/failed) counts as on, so password fields never vanish
// on a slow or failed config fetch — the backend enforces either way.
export function useCredentialLogin(): boolean {
  return useAuthConfig().data?.credentialLogin ?? true
}

// useIsLocal reports whether the gateway runs in local mode (one owner on
// this computer: no sign-in, no other users). Unknown or absent counts as
// server mode, so nothing is hidden on a slow or failed config fetch.
export function useIsLocal(): boolean {
  return useAuthConfig().data?.mode === "local"
}
