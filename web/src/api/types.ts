// Shared shapes of the gateway's /api JSON responses.

export interface UserView {
  id: number
  name: string
  email: string
  role: "superadmin" | "admin" | "user"
  disabled: boolean
  createdAt: number
  keyCount?: number
  // true once the user has signed in with Google; their name then comes from
  // the Google profile and can't be edited.
  googleLinked: boolean
  // Google profile picture; omitted unless the user signed in with Google and
  // has one.
  avatarUrl?: string
}

// GET /api/auth/config (unauthenticated). google is omitted when Google
// sign-in isn't configured; domain is omitted when there's no Workspace
// restriction. credentialLogin is the effective value (always true without
// Google).
export interface GoogleAuthConfigView {
  clientId: string
  domain?: string
}

export interface AuthConfigView {
  credentialLogin: boolean
  google?: GoogleAuthConfigView
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

// One provider row annotated with a user's access state (Users page access
// dialog). disabledForUser only matters when enabled is true — a globally
// disabled provider is unroutable for everyone.
export interface UserProviderView {
  id: number
  name: string
  preset: string
  enabled: boolean
  disabledForUser: boolean
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
  preset: string
  sortOrder: number
  healthy: number
  total: number
  keys: ProviderKeyView[]
}

// A curated preset from GET /api/providers/presets — release-managed
// endpoints the GUI offers in the "Add Provider" dropdown.
export interface PresetSpecView {
  id: string
  label: string
  openaiBaseUrl: string
  anthropicBaseUrl: string
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

// --- usage monitoring v2 (GET /api/usage/overview|breakdown|series|requests|facets) ---

export type UsageSurface = "chat" | "messages" | "responses" | "mcp"
export type UsageStatusFilter = "ok" | "error"
export type UsageBucket = "hour" | "day"
export type SortOrder = "asc" | "desc"

// Effective range echoed by every v2 endpoint — differs from the request
// when the server capped the span (92 days max).
export interface UsageRange {
  from: number
  to: number
}

// inputTokens is the total prompt size INCLUDING cached input; cachedTokens is
// the cached subset (0 on rows recorded before cache tracking / when unknown).
export interface UsageOverviewTotals {
  requests: number
  errors: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  avgDurationMs: number
  p95DurationMs: number
  avgTtfbMs: number
  users: number
  models: number
}

export interface UsageOverviewPoint {
  ts: number
  requests: number
  errors: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  avgDurationMs: number
}

export interface UsageOverview {
  range: UsageRange
  bucket: UsageBucket
  totals: UsageOverviewTotals
  points: UsageOverviewPoint[]
}

export type UsageBreakdownGroupBy =
  | "user"
  | "model"
  | "user_model"
  | "provider"
  | "key"
  | "upstream_key"

export type UsageBreakdownSort =
  | "requests"
  | "errors"
  | "errorRate"
  | "inputTokens"
  | "cachedTokens"
  | "outputTokens"
  | "totalTokens"
  | "avgDurationMs"
  | "lastTs"
  | "label"

// Fields not relevant to the grouping are zero/empty.
export interface UsageBreakdownRow {
  userId: number
  email: string
  provider: string
  model: string
  keyId: number
  alias: string
  upstreamKey: string
  requests: number
  errors: number
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  avgDurationMs: number
  avgTtfbMs: number
  firstTs: number
  lastTs: number
}

export interface UsageBreakdownTotals extends UsageTotals {
  cachedTokens: number
}

export interface UsageBreakdown {
  range: UsageRange
  groupBy: UsageBreakdownGroupBy
  page: number
  pageSize: number
  total: number
  totals: UsageBreakdownTotals
  rows: UsageBreakdownRow[]
}

export type UsageSeriesGroupBy = "user" | "model" | "provider" | "surface"
export type UsageSeriesMetric = "requests" | "totalTokens" | "cachedTokens" | "errors"

export interface UsageSeriesPoint {
  ts: number
  value: number
}

// key: user → "<userId>", model → "<provider>/<model>", provider/surface →
// the name; everything outside the top N is folded into "other".
export interface UsageSeriesLine {
  key: string
  label: string
  points: UsageSeriesPoint[]
}

export interface UsageSeries {
  range: UsageRange
  bucket: UsageBucket
  groupBy: UsageSeriesGroupBy
  metric: UsageSeriesMetric
  series: UsageSeriesLine[]
}

export type UsageRequestSort = "ts" | "durationMs" | "totalTokens" | "cachedTokens"

export interface UsageRequestRow {
  id: number
  ts: number
  userId: number
  email: string
  keyId: number
  alias: string
  provider: string
  model: string
  surface: string // "" on pre-V6 rows
  stream: boolean
  status: number
  upstreamStatus: number
  failReason: string
  upstreamKey: string // blanked for non-admins
  inputTokens: number
  cachedTokens: number
  outputTokens: number
  durationMs: number
  ttfbMs: number
  attempts: number
}

export interface UsageRequests {
  range: UsageRange
  page: number
  pageSize: number
  total: number
  rows: UsageRequestRow[]
}

export interface UsageFacets {
  users: { id: number; email: string }[] // empty for non-admins
  providers: string[]
  models: { provider: string; model: string }[]
  keys: { id: number; alias: string; userId: number }[]
  surfaces: string[]
}

// --- runtime settings (GET/PUT /api/settings) ---

// models.dev-shaped reasoning controls (mirrors settings.ReasoningOption).
// Only "type" is always present: "effort" carries a "values" ladder,
// "budget_tokens" a min/max range, "toggle" neither.
export interface ReasoningOptionView {
  type: string
  values?: string[]
  min?: number
  max?: number
}

// --- model catalog (GET /api/models) ---

// One Design Arena result for a model (Elo per arena/category).
export interface DesignArenaScoreView {
  arena: string
  category: string
  elo: number
  win_rate: number
  rank: number
}

// Benchmark scores matched to a catalog entry (OpenRouter benchmark data).
// Artificial Analysis indexes are 0–100, higher is better; each is omitted
// when not published.
export interface ModelBenchmarksView {
  source_id: string // OpenRouter model id the scores belong to
  source_name?: string
  intelligence_index?: number
  coding_index?: number
  agentic_index?: number
  design_arena?: DesignArenaScoreView[]
}

// fetched_at: unix seconds of the last successful fetch (0 = never);
// error: set when the last attempt failed.
export interface BenchmarksStatusView {
  fetched_at: number
  count: number
  error?: string
}

// One merged catalog entry; id is "provider/model" (with cosmetic suffixes).
// benchmarks is present only when the model matched benchmark data.
export interface CatalogModelView {
  id: string
  owned_by?: string
  context_window?: number
  max_output_tokens?: number
  input_modalities?: string[]
  reasoning?: boolean
  reasoning_options?: ReasoningOptionView[]
  responses_api?: boolean
  free?: boolean
  description?: string
  benchmarks?: ModelBenchmarksView
}

export interface CatalogModelsView {
  object: string
  data: CatalogModelView[]
  benchmarks_status?: BenchmarksStatusView
}

export interface ModelMetaView {
  contextWindow: number
  maxOutputTokens: number
  reasoning: boolean
  responsesApi: boolean
  inputModalities?: string[]
  description?: string
  reasoningOptions?: ReasoningOptionView[]
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
  // Sync-managed (models.dev) — only the reasoning facts: Kilo's own catalog
  // carries the limits/modalities, it never states reasoning levels.
  modelMeta?: Record<string, ModelMetaView>
}

// Sync-managed (models.dev, same sync as zen's) — Z.ai's /models only lists
// ids, so preset providers enrich from this catalog. Saved back untouched.
export interface ZaiSettingsView {
  modelMeta?: Record<string, ModelMetaView>
}

export interface AnthropicSettingsView {
  fallbackModel: string
}

// /mcp web tools (search via SearXNG, read via native fetch + obscura).
export interface WebToolsSettingsView {
  enabled: boolean
  searxngUrl: string
  readerMode: "fast" | "render"
  obscuraPath: string
  obscuraStealth: boolean
  obscuraConcurrency: number
  fetchTimeoutSeconds: number
  obscuraTimeoutSeconds: number
  maxChars: number
}

// Sign-in options. credentialLogin=false disables email + password sign-in;
// the server refuses that unless Google sign-in is configured.
export interface AuthSettingsView {
  credentialLogin: boolean
}

// Defaults applied when an admin creates a regular user: the new user is
// denied every provider listed here (empty = every provider allowed).
// Admins and superadmins always get every provider; existing users are never
// affected. The server prunes ids of deleted providers.
export interface NewUsersSettingsView {
  disabledProviderIds: number[]
}

export interface RuntimeSettingsView {
  auth: AuthSettingsView
  rotation: "priority-daily" | "priority" | "lru"
  retry: RetrySettingsView
  anthropic: AnthropicSettingsView
  zen: ZenSettingsView
  kilo: KiloSettingsView
  zai: ZaiSettingsView
  webTools: WebToolsSettingsView
  // always sent by current servers; absent on older ones (treat as [])
  newUsers?: NewUsersSettingsView
}

// POST /api/webtools/test response (admin diagnostics).
export interface WebToolsTestResult {
  ok: boolean
  latencyMs?: number
  version?: string
  detail?: string
  nextStep?: string
}

// Z.ai coding-plan per-key usage (GET /api/providers/{id}/keys/{keyId}/usage
// passes the upstream quota object through verbatim).
export interface ZaiUsageDetail {
  modelCode: string
  usage: number
}

export interface ZaiUsageLimit {
  type: string // TOKENS_LIMIT | CREDIT_LIMIT | TIME_LIMIT
  number?: number // CREDIT_LIMIT subtype: 5 = the 5-hour credit window
  percentage?: number
  currentValue?: number
  usage?: number
  nextResetTime?: number // epoch ms
  usageDetails?: ZaiUsageDetail[]
}

export interface ZaiUsage {
  level: string // plan tier: lite | pro | max
  limits: ZaiUsageLimit[]
}

// GET /api/about — the running build and the changelog (any signed-in user).
// intro and section items are inline markdown (code, bold, italic, links).
export interface ReleaseSectionView {
  title: string
  items: string[]
}

export interface ReleaseNotesView {
  version: string // "Unreleased" or a bare semver like "0.1.1"
  date: string // "" for Unreleased
  url: string // compare/release link; may be ""
  intro: string // paragraphs separated by a blank line; may be ""
  sections: ReleaseSectionView[]
}

export interface AboutView {
  name: string
  version: string // raw build version; "dev" for local builds
  displayVersion: string // compact sidebar label, e.g. "v0.1.1+48"
  baseVersion: string // latest release this build is based on; "" when unknown
  commitsAhead: number // commits after baseVersion; 0 = exactly the release
  commit: string // "" when unknown
  buildDate: string // RFC3339; "" when unknown
  repository: string
  license: string
  releases: ReleaseNotesView[] // newest first; "Unreleased" first when present
}
