# API reference

## Authentication

All `/v1/*` endpoints require a client key:

```text
Authorization: Bearer fg-...
x-api-key: fg-...          (also accepted)
```

Client keys are created by their owner in the GUI (Profile → My API Keys — nobody, not even a superadmin, creates keys for someone else),
stored sha256-hashed server-side, and cached in memory — revocation applies
to the very next request. `GET /health` is open.

### Local mode

In [local mode](local.md) (`nano-llm-proxy local`) authentication works
differently:

- `/api/*` needs no session: every request acts as the single owner (a
  superadmin). `POST /api/auth/login` and `POST /api/auth/google` answer
  404, `POST /api/auth/logout` answers `{"ok": true}` and does nothing.
  State-changing `/api/*` requests keep the same-origin Origin check (403);
  `NANO_TRUSTED_ORIGINS` is ignored.
- `/v1/*` and `/mcp` accept a missing or unknown key and bill the request to
  the built-in `local (no key)` client key, unless the `local.requireApiKey`
  setting is on (then 401 as in server mode). A valid `fg-…` key is used as
  itself. With `local (no key)` disabled, keyless requests get a 401.
- `/v1/*` and `/mcp` answer 403 when an `Origin` header names a host other
  than `localhost`, `127.0.0.1` or `::1`, and 415 for a `POST` whose
  `Content-Type` media type isn't exactly `application/json` (parameters
  such as `charset` are fine). These errors use the `{"error":{"message"}}`
  shape on every surface.
- Every route, `/health` and the GUI included, answers 403 (plain text) when
  the `Host` header isn't `localhost`, `127.0.0.1` or `::1` with the
  gateway's port.

## POST /v1/chat/completions

OpenAI Chat Completions shape, passthrough plus key rotation. Streaming
requests (`"stream": true`) proxy the SSE stream; non-streaming requests get
one aggregated JSON response (streaming is always used upstream where the
adapter requires it).

```bash
curl -N http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer fg-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "openai/gpt-4o-mini",
    "stream": true,
    "max_tokens": 500,
    "messages": [{"role":"user","content":"hello"}]
  }'
```

Tool calls, tool results, temperatures, and the rest of the request body
pass through. Requests that translate across API surfaces (see
[the README's routing section](../README.md#model-routing)) keep tool-call
transcripts intact both ways.

## POST /v1/responses

Native OpenAI Responses API passthrough for models served on that surface.
Only zen has a Responses API: any other provider prefix (or a bare model
name) is a 400 — use `/v1/chat/completions` for those.

```bash
curl http://localhost:8787/v1/responses \
  -H "Authorization: Bearer fg-..." \
  -H "Content-Type: application/json" \
  -d '{"model":"zen/<responses-surface-model>","input":"hi","max_output_tokens":600}'
```

`"stream": true` proxies the genuine Responses event stream (reasoning
events included); `stream: false` returns an aggregated `response` object.
A model that lives on the chat surface is rejected here — call it through
`/v1/chat/completions` instead, where the gateway translates.

## POST /v1/messages

Anthropic Messages protocol — for Claude Code and any Anthropic-shaped
client. Supported:

- top-level `system` (string or content blocks)
- `tools[].input_schema` → upstream tool schemas; `tool_use` / `tool_result`
  blocks in the transcript (arguments re-serialized, streamed fragments
  merged)
- full SSE event sequence: `message_start`, `content_block_start/delta/stop`,
  `message_delta` (stop reasons `end_turn`, `tool_use`, `max_tokens`),
  `message_stop`
- non-streaming clients get one JSON message object
- `usage` in Anthropic semantics: `input_tokens` excludes cache hits,
  `cache_read_input_tokens` carries them (always present, `0` when the
  upstream reported none) and `cache_creation_input_tokens` is `0` — so
  `input_tokens + cache_read_input_tokens + cache_creation_input_tokens` is
  the upstream's total prompt (OpenAI `prompt_tokens`). Streaming
  `message_start` carries zeros (translated upstreams report usage only at
  the end); the final figures, `output_tokens` included, ride on
  `message_delta`.

```bash
curl http://localhost:8787/v1/messages \
  -H "Authorization: Bearer fg-..." \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-5",
    "max_tokens": 300,
    "messages": [{"role":"user","content":"hello"}]
  }'
```

Model resolution order: a literal `claude-*` name (no explicit
`provider/` prefix) routes to the configured fallback target
(Settings → Anthropic fallback) — the safety net for the background calls
clients self-issue; anything else routes directly. Zen, Kilo and Z.ai IDs
may carry the cosmetic context/modality suffix (`zai/glm-5.3-flash-1M-txt-img-vid-pdf`),
which is stripped for those providers; a `[1m]` suffix is stripped for
every provider before the upstream call.

When the routed provider has an Anthropic-compatible endpoint
(`anthropicBaseUrl`), the request is forwarded to it natively — none of
the translation above applies: thinking blocks, `anthropic-beta` headers,
interleaved `system` messages, and SSE event framing pass through
verbatim, and only the credential is swapped for a pooled upstream key.
Providers with only an OpenAI-compatible root go through the translation
described above.

## GET /v1/models

Merged catalog of every enabled provider the caller may use, enriched per
entry. With per-user provider access (Admin API below) the catalog is
scoped per client key: entries from a provider revoked for the key's owner
are left out, so clients only ever see routable models:

A Kilo entry (Kilo's catalog is the richest; zen and Z.ai entries carry the
same limit/modality/reasoning fields, plus `responses_api` and `free` for
zen; custom providers pass through only what their upstream lists):

```json
{
  "id": "kilo/inclusionai/ling-3.1-flash-262K-txt",
  "object": "model",
  "context_window": 262144,
  "context_length": 262144,
  "max_output_tokens": 32768,
  "input_modalities": ["text"],
  "reasoning": true,
  "supported_parameters": ["tools", "temperature"]
}
```

`context_length` mirrors `context_window` — different agents read different
spellings, and a missing field reads as "unknown, assume 128K".

A reasoning model also carries `reasoning_options`, the models.dev-shaped
control list an agent (oh-my-pi) needs to offer actual levels — `reasoning:
true` alone says nothing about which values the model accepts:

```json
{
  "id": "zen/ling-3.1-flash-free-262K-txt",
  "object": "model",
  "owned_by": "zen",
  "context_window": 262144,
  "context_length": 262144,
  "max_output_tokens": 32768,
  "input_modalities": ["text"],
  "reasoning": true,
  "reasoning_options": [
    {"type": "effort", "values": ["low", "medium", "high"]}
  ],
  "responses_api": false,
  "free": true,
  "description": "Efficient model for low-latency assistance"
}
```

Option types mirror models.dev: `effort` carries a `values` ladder, `toggle`
nothing (reasoning on or off), `budget_tokens` a `min`/`max` range. Options
come from the models.dev catalog; a zen/zai reasoning model the catalog
doesn't document yet falls back to `effort` with `low`/`medium`/`high`, and a
model flagged non-reasoning never gets the field at all.

Kilo entries resolve their reasoning fields from the catalog too
(`kilo.modelMeta`), keyed by Kilo's own ids — `:free` suffix included.
Kilo's upstream numbers (context/output limits, modalities,
`supported_parameters`) always win; only the reasoning facts come from the
catalog. A kilo model models.dev doesn't document keeps both fields absent
rather than advertising an unverified `false`.

Image input is passed through on every path: chat `image_url` parts, and
Anthropic `image` blocks from `/v1/messages` (Claude Code), reach the upstream
as valid content parts on both the chat and Responses surfaces. The entry's
modalities are also published in every spelling clients read — `input_modalities`
(opencode), `input` (pi / oh-my-pi) and `architecture.input_modalities`
(pi's opencode provider loader) — so an agent knows a model takes images
instead of assuming text-only and dropping the picture.

The ID suffix is cosmetic (context + input modalities); bare IDs are
accepted on every endpoint. Z.ai entries are enriched from a models.dev-synced
catalog; a model the catalog doesn't know yet still advertises a 1M context
window so agents don't downshift to 128K.

Each entry also carries a nested `limits` object
(`{"max_input_tokens": …, "max_output_tokens": …}`) alongside the top-level
spellings. Some OpenAI-compatible discovery clients (oh-my-pi among them) read
the output budget *only* from that object and fall back to a fixed 33K when it
is missing, while reading the context window from the top-level field — so a
catalog without `limits` shows every model as 33K max-out. It is derived from
the same enriched values as `context_window` / `max_output_tokens`, and entries
with no advertised output limit omit it rather than claiming zero.

Note for oh-my-pi specifically: its `openai-models-list` discovery resolves
reasoning and thinking levels from its own bundled catalog by model id, never
from this payload. Ids carrying the cosmetic suffix therefore never match, and
the gateway cannot fix that field from the server side — declare it in
`~/.omp/agent/models.yml` under the provider's `modelOverrides` instead.

## GET /health

Open, no auth. `providers` maps each provider to its upstream key-pool
counts (`total` keys, `healthy` of them — the rest cooling or disabled);
`webtools` reports whether the MCP web tools are enabled:

```json
{"status": "ok", "uptime_s": 3600, "providers": {"zen": {"total": 14, "healthy": 14}, "openai": {"total": 3, "healthy": 3}}, "webtools": {"enabled": true}}
```

`providers` maps every enabled provider (the built-in `zen` plus each
GUI-added provider under its own name) to its upstream key-pool counts.

## POST /mcp

An MCP (Model Context Protocol) server over streamable HTTP, running
stateless (no `Mcp-Session-Id`, JSON responses instead of SSE). It exposes
two self-hosted web tools to every client key; requires `webTools.enabled`
in Settings (otherwise 404). Auth is the same as `/v1/*`: `Authorization:
Bearer fg-…` or `x-api-key`.

Tools:

- **`web_search`** `{query: string, maxResults?: int (1–25, default 8)}` —
  searches via the gateway's SearXNG instance; returns a numbered markdown
  list (title, URL, snippet, engines).
- **`web_read`** `{url: string, render?: bool, maxChars?: int}` — fetches a
  page and returns its main content as markdown. Plain pages use a fast
  native fetch; JavaScript-heavy or bot-protected pages are rendered with
  the obscura headless browser (set `render: true` when you know the page
  needs a browser, or when plain content comes back empty). Private/loopback
  addresses are refused (SSRF protection).

The optional scalars (`maxResults`, `render`, `maxChars`) are advertised as
`["integer","string"]` / `["boolean","string"]` and coerced server-side, so
a client that stringifies tool arguments still gets the value it asked for.
This is deliberate: MCP clients validate arguments against the advertised
schema *before* sending the request, so a scalar-typed property makes those
clients reject the call with `must be integer` / `must be boolean` and the
tool never runs. Values outside the union (say `maxResults: "lots"`) are
still rejected, as a tool error naming the argument.

Tool-level failures (site unreachable, blocked URL, SearXNG down) come back
as MCP tool errors (`isError: true`) with actionable text, so agents can
adapt — they don't break the protocol session. Each tool call is metered as
one `web-tools` usage event (provider `web-tools`, model = tool name); a
blocked/invalid URL meters as an error. Protocol POSTs (initialize,
notifications) count toward the client key's request counter like any other
authenticated request.

Setup for clients: `claude mcp add -s user -t http nano-web
https://<gateway>/mcp --header "Authorization: Bearer fg-…"` — the GUI's
Profile → My API Keys → "Set up MCP web tools" dialog generates this plus
opencode/Kilo/Pi configs, and the Guide page renders full per-agent setup
(Pi supports custom OpenAI-compatible endpoints natively via
`~/.pi/agent/models.json` and has built-in MCP via `pi mcp add`, so no
extension packages are needed). Backends are installed with
`scripts/install-web-tools.sh` (see `docs/deployment.md`).

## Admin API (`/api/*`)

Used by the embedded GUI; session-cookie authenticated with bcrypt-backed
logins, per-IP+email backoff, and Origin checks on state-changing requests.
Roles: `superadmin` and `admin` (everything below — "admin" in the table
means either) and `user` (own keys, profile, dashboard, models, usage and
guide). The one difference between
the two admin roles: an admin can't change a superadmin account in any way
(deactivate, delete, rename, change role, reset password) and can't grant the
`superadmin` role. Superadmins can
change other superadmins. Nobody can deactivate or delete their own account
(409).

| Endpoint | Role | Purpose |
| --- | --- | --- |
| `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/me` | — | Session lifecycle. Login answers 403 when password sign-in is disabled (`auth.credentialLogin`) |
| User objects | — | Every user payload (`/api/auth/me`, `/api/users`, login, `PATCH /api/me`) carries `googleLinked` (has signed in with Google — name is Google-owned and refreshed on each Google sign-in; admins can't rename it either) and, for Google users with a photo, `avatarUrl` (an `https://*.googleusercontent.com` URL refreshed on each Google sign-in) |
| `GET /api/about` | user | The running build and the changelog for the GUI's About page: `name`, `version` (raw `git describe` stamp, `dev` for local builds), `displayVersion` (compact, e.g. `v0.1.1+48`, `v0.3.0-rc.1`), `baseVersion` (the tag it builds on, pre-release suffix included), `prerelease` (that tag is a pre-release such as `0.3.0-rc.1`; its notes are under Unreleased), `commitsAhead`, `commit`, `buildDate`, `repository`, `license`, and `releases` — `CHANGELOG.md` (embedded in the binary) parsed into `{version, date, url, intro, sections: [{title, items}]}`, newest first; items are inline Markdown |
| `GET /api/auth/config` | — (open) | Sign-in methods for the login page: `{"credentialLogin": bool, "mode": "server" \| "local", "google": {"clientId", "domain"?}}`; `mode` is always present; `google` is omitted when Google sign-in isn't configured, and always in local mode |
| `POST /api/auth/google` | — | Google sign-in: body `{"credential": "<Google ID token>"}`; verifies the token (signature, issuer, audience, expiry, verified email, Workspace domain) and starts the same session as password login. Origin-checked and backoff-limited. 400 no credential, 401 bad token, 403 outside the domain / no account with this email (Google sign-in never creates accounts — add the user on the Users page) / disabled account / cross-origin, 404 when not configured, 429 too many failed attempts from this IP, 503 Google's signing keys unreachable |
| `GET/POST /api/me/keys`, `PATCH/DELETE /api/me/keys/{keyId}` | user | Own client keys |
| `GET/POST /api/users`, `PATCH/DELETE /api/users/{id}` | admin | User management. `POST` requires a password (min 10 characters) only while password sign-in is on; with it off, users are created without one and sign in with Google. A new `user`-role account starts with the providers listed in the `newUsers.disabledProviderIds` runtime setting revoked (written atomically with the user; ids of deleted providers are skipped); `admin`/`superadmin` accounts get no revocations. `PATCH` takes any of `name`, `email`, `role`, `password` (min 10 characters; ends that user's sessions) and `disabled`. `PATCH {"disabled": true}` deactivates a user: their sessions end and their client keys stop authenticating until reactivated. Demoting, deactivating or deleting the last active superadmin is 409; deactivating or deleting yourself is 409; renaming a Google-linked user is 400; an admin changing a superadmin account, or granting the superadmin role, is 403 |
| `GET /api/users/{id}/keys` | admin | Read-only view of a user's client keys. Nobody — not even a superadmin — creates, toggles or deletes another user's keys; owners manage their own via `/api/me/keys`, and deactivating a user turns all of theirs off |
| `GET /api/users/{id}/providers`, `PUT /api/users/{id}/providers/{providerId}/access` | admin | Per-user provider access. The listing annotates each provider with `disabledForUser`; the PUT takes `{disabled: bool}` and answers 409 for an admin or superadmin target — they always have every provider (stray revocations for them are ignored, and promotion clears a user's revocations). Access is allow-by-default: a revoked provider vanishes from that user's `/v1/models` and their requests to it fail exactly like an unknown provider |
| `GET/POST /api/providers`, `PATCH/DELETE /api/providers/{id}` | admin | Providers (generic CRUD; `baseUrl` and/or `anthropicBaseUrl`, at least one required; built-ins cannot be deleted). Create takes `keys` as `{key, label}` objects — every upstream key needs a label (1-64 characters). It also accepts a `preset` id: unknown presets are 400, the name defaults to the preset id, endpoint roots default from the registry, and the preset is immutable afterwards. Multiple providers per preset are allowed; only `zen` is reserved and undeletable |
| `GET /api/providers/presets` | admin | The curated preset registry (id, label, endpoint roots) behind the GUI's Add Provider dropdown |
| `POST /api/providers/{id}/keys`, `PATCH/DELETE /api/providers/{id}/keys/{keyId}` | admin | Upstream keys; POST requires a label, PATCH may rename a key but never blank its label. Every new key — added here or among the initial keys of `POST /api/providers` — is verified against each endpoint the provider has (OpenAI: `GET /models` plus a one-token call for a nonexistent model; Anthropic: `GET /v1/models` plus the same via `/v1/messages`; OpenRouter additionally `GET /api/v1/key`) before anything is saved. Per probe: 401, or a 400 whose message says the key is wrong (xAI answers a wrong key that way) → 400 `Key "<label>" is invalid — …`; 403 (except zen's `FreeTierError`) → 400 `Key "<label>" was refused — …` (the key may be genuine, but this server can't use it: region, IP allowlist, permissions, a blocked account or budget); unreachable, timeout or 5xx → 503, nothing saved; anything else (2xx, other 4xx including 402/429) means the key authenticated. The fake model id means no inference runs and no quota is spent; upstream text is shown with the key redacted. A key already on the provider is a 409 (checked before any probe), the same key twice in one create is a 400 |
| `POST /api/providers/{id}/keys/import` | admin | Bulk key import. Body `{"content": "<file text>"}`; the file must be one JSON object of `"alias": "key"` string pairs, e.g. `{"work-1": "sk-…", "work-2": "sk-…"}` — at most 200 entries. File order is kept: imported keys are appended to the rotation after the provider's existing keys. The whole request is a 400 with nothing saved and nothing probed when the text isn't valid JSON, isn't a single object (or has data after it), a value isn't a string, an alias appears twice, the object is empty, or it holds more than 200 entries; unknown provider → 404. Otherwise every entry is processed on its own (one bad key never sinks the rest) and the answer is 200 `{"added": n, "results": [{"label", "status", "message"}]}`, one result per entry in file order. `status`: `added` (message empty); `rejected` (empty key, or an alias that isn't 1-64 characters); `duplicate` (the same key earlier in the file, or already on the provider); `invalid` / `refused` / `unverifiable` — the key verification described above, up to 4 keys at a time, with the same messages a single add returns. All `added` keys are saved in one write and go live in the same request; key material is never echoed |
| `GET /api/providers/{id}/keys/{keyId}/usage` | admin | Per-key quota/usage from the provider's preset usage endpoint, fetched on demand (Z.ai coding plans: plan tier, 5-hour and weekly windows, MCP). The upstream `data` object passes through verbatim; presets without a usage endpoint answer 400; an unreachable or failing upstream answers 503 |
| `GET /api/settings` | admin | Effective runtime settings document (rotation, retries, Claude fallback, adapter knobs, model catalog, new-user defaults, local mode). `newUsers` is `{"disabledProviderIds": [<provider id>, ...]}` — always a list (`[]` = new users get every provider), ids of deleted providers omitted. `local` is `{"requireApiKey": bool}`, present in both modes and used only in local mode |
| `PUT /api/settings` | admin | Validate and replace the document; omitted fields fall back to defaults, the models.dev sync status is server-owned and preserved. Validation failures return 400 and change nothing. Turning `auth.credentialLogin` off is a 400 when Google sign-in isn't configured, or when the acting admin is outside `GOOGLE_WORKSPACE_DOMAIN` (they couldn't sign back in). `newUsers.disabledProviderIds` is de-duplicated and sorted; a non-positive id is a 400, an id of a provider that doesn't exist is dropped silently. Changing it affects only users created afterwards. Applies in-request — pool mode and the `/v1/models` cache refresh before the response |
| `POST /api/settings/model-meta/sync` | admin | Run the models.dev catalog sync and the benchmark fetch now; returns the sync status and `benchmarks_status` (200 even when `ok:false` — the errors are in the status blocks; neither sync depends on the other) |
| `GET /api/local` | admin | Local mode only (404 in server mode). This computer: `{"platform", "port", "dataDir", "logFile", "binary", "autostart": {"supported", "enabled", "running", "error"}, "searxng": {"installed", "running", "url"}}`. `autostart.enabled` = registered to start at sign-in, `running` = running as that background service, `error` = the service manager's error text (`""` when none); `searxng.installed` = the installer's SearXNG exists in the data folder, `url` = the `webTools.searxngUrl` setting; `platform` = the gateway's OS (`linux`, `darwin`, `windows`) |
| `PUT /api/local/autostart` | admin | Local mode only (404 in server mode). Body `{"enabled": bool}`: registers or removes the start-at-sign-in entry. Takes effect at the next sign-in; it doesn't stop or start the running instance. Returns the `GET /api/local` object. 400 without `enabled` or when this OS has no mechanism, 500 when the service manager fails |
| `PUT /api/local/port` | admin | Local mode only (404 in server mode). Body `{"port": int}` (1024–65535, else 400; 409 when another program holds it). Saves `NANO_PORT` to the data folder's `.env`. Running as a background service that systemd or launchd restarts (Linux with a systemd user instance, macOS): `{"restarting": true, "url"}`, then the process exits so the service manager starts it on the new port. Otherwise (a foreground run, Windows, the Linux desktop-autostart fallback): `{"restarting": false, "url", "message"}` — restart it yourself |
| `GET /api/dashboard` | user | Pool health and recent activity. Admins get per-key pool views and the full activity ring; other users get health counts only (`keys: []`) and just their own activity entries, with the upstream `keyHash` blanked |
| `GET /api/models` | user | The merged, enriched model catalog `/v1/models` serves, plus benchmark scores — backs the Models page. Entries the benchmark source knows carry `benchmarks` (`source_id`, `source_name`, optional `intelligence_index`/`coding_index`/`agentic_index`, optional `design_arena` list of `{arena, category, elo, win_rate, rank}`); the body adds `benchmarks_status` (`fetched_at`, `count`, optional `error`). `/v1/models` never carries benchmarks |
| `GET /api/providers/{id}/models` | admin | One provider's live catalog, fetched on demand; 503 when the upstream can't be reached |
| `PUT /api/settings/responses-api` | admin | Flip the per-model Responses-API flag (`{model, responsesApi}`); sync preserves the flag |
| `POST /api/webtools/test` | admin | Live-probe a web-tools backend: `{target: "searxng" \| "obscura", deep?: bool}`. Runs one real search / `obscura --version` (deep adds an example.com fetch); returns `{ok, latencyMs, version?, detail, nextStep}` |
| `GET /api/usage/summary?from=&to=` | user | Requests/errors/tokens totals, top models, per-provider (scoped to own user; admins see all) |
| `GET /api/usage/keys?from=&to=` | user | Per-client-key requests/tokens in range (scoped) |
| `GET /api/usage/users?from=&to=` | admin | Per-user requests/tokens in range |
| `GET /api/usage/activity?limit=` | user | Persisted recent requests with token counts (scoped) |
| `GET /api/usage/timeseries?from=&to=&bucket=&tz=` | user | Per-bucket requests/tokens (`points`) and per-provider requests (`providers`) for the dashboard charts; `bucket` (seconds) is echoed. `bucket=hour\|day` (default hour when the span is ≤ 48h, else day); `tz` = seconds east of UTC (scoped) |
| `GET /api/usage/overview` | user | Usage monitoring totals (incl. p95 latency, avg TTFB, distinct users/models) and a zero-filled per-bucket series — see [Usage monitoring](#usage-monitoring-apiusage) |
| `GET /api/usage/breakdown` | user | Grouped, paged, sortable usage table (`groupBy=user\|user_model\|upstream_key` are admin-only) |
| `GET /api/usage/series` | user | Top-N series per group with the rest folded into `other` (`groupBy=user` is admin-only) |
| `GET /api/usage/requests` | user | Paged raw request log with upstream detail |
| `GET /api/usage/facets` | user | Distinct users/providers/models/keys/surfaces in the range, for filter dropdowns |
| `POST /api/me/password` | user | Self-service password reset; requires the current password and wipes all sessions. 403 for Google-linked accounts, which sign in with Google |
| `PATCH /api/me` | user | Rename yourself: body `{"name"}` (trimmed, 1–100 characters) → `{"user"}`. 403 for Google-linked accounts, whose name comes from Google |

Every mutation writes to SQLite and rebuilds the in-memory pools/key cache
in the same request. Usage ranges are unix seconds, capped to 92 days;
`to` is clamped to now and negative bounds are a 400;
token counts come from whatever the upstream reports (0 when it reports
none). `inputTokens` is always the TOTAL prompt including cache hits and
`cachedTokens` the cache-read subset of it (OpenAI
`prompt_tokens_details.cached_tokens` / Responses
`input_tokens_details.cached_tokens`); native Anthropic usage is normalized
to the same meaning (`inputTokens` = `input_tokens` +
`cache_read_input_tokens` + `cache_creation_input_tokens`, `cachedTokens` =
`cache_read_input_tokens`). Rows recorded before cache tracking (migration
V7) read `cachedTokens: 0`. Events persist in the `usage_events` table and are pruned after 90
days.

### Usage monitoring (`/api/usage/*`)

Each usage event records the client surface (`chat`, `messages`,
`responses`, `mcp`), whether the client streamed, how many upstream calls
were made, the last upstream HTTP status (`0` = none or a network error),
the label of the upstream key that served (or was last tried), the time from
request start to the first upstream `200`, the client-facing hint on failure
(≤ 300 characters), and snapshots of the user's email and key alias. Rows
written before this detail existed carry empty/zero values. Requests rejected
before routing (malformed model string, unknown or revoked provider, wrong
surface on `/v1/responses`) are recorded too, with the 4xx status the client
got and its message as the fail reason.

All five endpoints below are `GET`, need a session, and answer bad
parameters with `400` in the standard error shape. Non-admins are
always scoped to their own user: passing someone else's `userId`, or a
admin-only grouping, is `403`.

Common filters (overview, breakdown, series, requests):

| Param | Notes |
| --- | --- |
| `from`, `to` | unix seconds; default last 24h; negative values are a 400; `to` is clamped to now and the span is capped at 92 days (the effective range is echoed as `range`) |
| `tz` | seconds east of UTC, -86400..86400; aligns buckets to the viewer's midnight |
| `userId` | admins only (others are forced to themselves) |
| `keyId` | client key id |
| `provider`, `model` | exact provider name / upstream model id |
| `surface` | `chat` \| `messages` \| `responses` \| `mcp` |
| `status` | `ok` (status < 400) \| `error` |
| `bucket` | `hour` \| `day`; default hour when the span is ≤ 48h, else day |

User labels prefer the live email, then the request-time snapshot, then
`deleted user #<id>`; key aliases likewise (`deleted key #<id>`).

**`GET /api/usage/overview`** —
`{range, bucket, totals: {requests, errors, inputTokens, outputTokens, cachedTokens, avgDurationMs, p95DurationMs, avgTtfbMs, users, models}, points: [{ts, requests, errors, inputTokens, outputTokens, cachedTokens, avgDurationMs}]}`.
`avgTtfbMs` averages only rows with a recorded TTFB; `p95DurationMs` is the
nearest-rank p95 over successful rows; `users`/`models` are distinct counts
(models keyed on provider + model). `points` covers every bucket in the
range, zero-filled.

**`GET /api/usage/breakdown`** — `groupBy` = `model` (default, keyed on
provider + model) \| `provider` \| `key` \| `user` \| `user_model` \|
`upstream_key` (the last three admin-only); `sort` = `requests`
(default) \| `errors` \| `errorRate` \| `inputTokens` \| `outputTokens` \|
`cachedTokens` \| `totalTokens` \| `avgDurationMs` \| `lastTs` \| `label`; `order` = `desc`
(default) \| `asc`; `page` (1-based); `pageSize` 1..200 (default 25); `q` =
case-insensitive substring of the group label (email, `provider/model`,
alias, ...; `%`/`_` match literally). Returns
`{range, groupBy, page, pageSize, total, totals: {requests, errors, inputTokens, outputTokens, cachedTokens}, rows: [{userId, email, provider, model, keyId, alias, upstreamKey, requests, errors, inputTokens, outputTokens, cachedTokens, avgDurationMs, avgTtfbMs, firstTs, lastTs}]}`.
`total` counts matching groups; `totals` sums every matching group, not just
the page. Fields outside the grouping are zero/empty (`key` rows also carry
the owning `userId`/`email`).

**`GET /api/usage/series`** — `groupBy` = `model` (default) \| `provider` \|
`surface` \| `user` (admin-only); `metric` = `requests` (default) \|
`totalTokens` \| `cachedTokens` \| `errors`; `top` 1..10 (default 5). Returns
`{range, bucket, groupBy, metric, series: [{key, label, points: [{ts, value}]}]}`:
the top groups by the metric (groups with a zero metric are omitted), then an
`other` series (label `Other`) folding the rest when there are more groups
than `top`. Keys: user → `"<userId>"` (label = email), model →
`"<provider>/<model>"`, provider/surface → the name. All series share the
same zero-filled bucket timestamps.

**`GET /api/usage/requests`** — `sort` = `ts` (default) \| `durationMs` \|
`totalTokens` \| `cachedTokens`; `order`; `page`; `pageSize` 1..200 (default 50). Returns
`{range, page, pageSize, total, rows: [{id, ts, userId, email, keyId, alias, provider, model, surface, stream, status, upstreamStatus, failReason, upstreamKey, inputTokens, outputTokens, cachedTokens, durationMs, ttfbMs, attempts}]}`.
`upstreamKey` is blank for non-admins.

**`GET /api/usage/facets`** — range params only. Returns
`{users: [{id, email}], providers: [..], models: [{provider, model}], keys: [{id, alias, userId}], surfaces: [..]}`;
`users` is empty for non-admins, whose other lists cover only their own
rows.

## Errors

| Status | Meaning |
| --- | --- |
| 400 | Unknown provider prefix, malformed request, or the upstream refused the request as sent (`upstream 4xx: …` — bad model id, unsupported parameter) |
| 401 | Missing/invalid client key |
| 503 | All keys cooling/disabled ("no healthy keys"), an upstream outage, or an upstream client-shape/version rejection — the body carries an actionable hint (e.g. raise the Zen user agent in Settings); retryable. `/v1/models` also answers 503 when every provider's catalog fetch fails |

The gateway never answers 502 (this applies to `/api/*` too): Cloudflare
(or a similar proxy) in front of a deployment replaces an origin's 502/504
body with its own page, which would hide the hint.

Error bodies follow the protocol being spoken:

```json
{"error": {"message": "No healthy zen keys available — every key is cooling or disabled"}}
```

on `/v1/chat/completions`, `/v1/responses`, `/v1/models` and `/api/*`, and

```json
{"type": "error", "error": {"type": "invalid_request_error", "message": "…"}}
```

on `/v1/messages` (`invalid_request_error` for a 400, `api_error` otherwise).

Failover across keys happens until the first response byte reaches the
client; after that, upstream failures propagate rather than corrupting a
stream mid-flight.
