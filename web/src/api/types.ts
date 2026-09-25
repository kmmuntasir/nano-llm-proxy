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
  anthropicBaseUrl: string
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

// --- usage reporting (GET /api/usage/*) ---

export interface UsageTotals {
  requests: number
  errors: number
  inputTokens: number
  outputTokens: number
}

export interface UsageModelRow {
  key: string
  requests: number
  inputTokens: number
  outputTokens: number
}

export interface UsageUserRow {
  userId: number
  email: string
  requests: number
  inputTokens: number
  outputTokens: number
}

export interface UsageKeyRow {
  keyId: number
  alias: string
  userId: number
  email: string
  requests: number
  inputTokens: number
  outputTokens: number
}

export interface UsageActivityRow {
  ts: number
  email: string
  alias: string
  provider: string
  model: string
  inputTokens: number
  outputTokens: number
  status: number
  durationMs: number
}

// --- runtime settings (GET/PUT /api/settings) ---

export interface ModelMetaView {
  contextWindow: number
  maxOutputTokens: number
  reasoning: boolean
  responsesApi: boolean
  inputModalities?: string[]
  description?: string
}

export interface ModelMetaSyncStatusView {
  at: number
  ok: boolean
  added: number
  updated: number
  pruned: number
  error?: string
}

export interface RetrySettingsView {
  maxKeysPerRequest: number
  cooldownSeconds: number
  respectRetryAfter: boolean
  maxRequestsPerKeyPerDay: number
}

export interface ZenSettingsView {
  userAgent: string
  injectTools: boolean
  freeOnly: boolean
  modelMeta?: Record<string, ModelMetaView>
  modelMetaAutoSync: boolean
  modelMetaSyncStatus?: ModelMetaSyncStatusView
}

export interface KiloSettingsView {
  freeOnly: boolean
}

export interface AnthropicSettingsView {
  fallbackModel: string
}

export interface RuntimeSettingsView {
  rotation: "priority" | "lru"
  retry: RetrySettingsView
  anthropic: AnthropicSettingsView
  zen: ZenSettingsView
  kilo: KiloSettingsView
}
