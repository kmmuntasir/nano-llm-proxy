// Shared shapes of the gateway's /api JSON responses.

export interface UserView {
  id: number
  name: string
  email: string
  role: "superadmin" | "user"
  disabled: boolean
  createdAt: number
  keyCount?: number
}

export interface ClientKeyView {
  id: number
  userId: number
  keyHint: string
  alias: string
  disabled: boolean
  createdAt: number
  lastUsedAt: number
  requestCount: number
}

export interface ProviderKeyView {
  id: number
  label: string
  sortOrder: number
  disabled: boolean
  createdAt: number
  hash?: string
  status?: string
  cooldownRemaining?: string
  requests?: number
  rateLimited?: number
  errors?: number
}

export interface ProviderView {
  id: number
  name: string
  type: "openai" | "opencode"
  baseUrl: string
  enabled: boolean
  builtin: boolean
  sortOrder: number
  healthy: number
  total: number
  keys: ProviderKeyView[]
}

export interface ActivityEntry {
  ts: number
  keyAlias: string
  user: string
  provider: string
  model: string
  keyHash: string
  status: number
  durationMs: number
  failReason?: string
}

export interface DashboardData {
  uptimeS: number
  providers: {
    name: string
    type: string
    enabled: boolean
    healthy: number
    total: number
    keys: {
      label: string
      hash: string
      status: string
      cooldown_remaining?: string
      requests: number
      rate_limited: number
      errors: number
    }[]
  }[]
  activity: ActivityEntry[]
}

export interface ApiErrorBody {
  error?: { message?: string }
}
