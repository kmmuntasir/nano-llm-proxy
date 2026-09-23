# API reference

## Authentication

All `/v1/*` endpoints require a client key:

```text
Authorization: Bearer fg-...
x-api-key: fg-...          (also accepted)
```

Client keys are created in the admin GUI (My Keys, or per-user under Users),
stored sha256-hashed server-side, and cached in memory — revocation applies
to the very next request. `GET /health` is open.

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

Native OpenAI Responses API passthrough for models served on that surface:

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
clients self-issue; anything else (including suffixed IDs like
`myprovider/my-model-128K`) routes directly. A `[1m]` suffix is stripped
before the upstream call.

## GET /v1/models

Merged catalog of every enabled provider, enriched per entry:

```json
{
  "id": "myprovider/my-model-128K-txt-img",
  "object": "model",
  "context_window": 131072,
  "max_output_tokens": 32768,
  "input_modalities": ["text", "image"],
  "reasoning": false,
  "responses_api": false,
  "supported_parameters": ["tools", "temperature"]
}
```

The ID suffix is cosmetic (context + input modalities); bare IDs are
accepted on every endpoint.

## GET /health

Open, no auth:

```json
{"uptime_s": 3600, "providers": {"openai": {"healthy": 2, "total": 3}}}
```

## Admin API (`/api/*`)

Used by the embedded GUI; session-cookie authenticated with bcrypt-backed
logins, per-IP+email backoff, and Origin checks on state-changing requests.
Roles: `superadmin` (everything below) and `user` (own keys + dashboard).

| Endpoint | Role | Purpose |
| --- | --- | --- |
| `POST /api/auth/login`, `POST /api/auth/logout`, `GET /api/auth/me` | — | Session lifecycle |
| `GET/POST /api/me/keys`, `PATCH/DELETE /api/me/keys/{keyId}` | user | Own client keys |
| `GET/POST /api/users`, `PATCH/DELETE /api/users/{id}` | superadmin | User management |
| `GET/POST /api/users/{id}/keys`, `PATCH/DELETE /api/users/{id}/keys/{keyId}` | superadmin | Per-user client keys |
| `GET/POST /api/providers`, `PATCH/DELETE /api/providers/{id}` | superadmin | Providers (generic CRUD; built-ins cannot be deleted) |
| `POST /api/providers/{id}/keys`, `PATCH/DELETE /api/providers/{id}/keys/{keyId}` | superadmin | Upstream keys |
| `GET /api/settings` | superadmin | Effective runtime settings document (rotation, retries, aliases, adapter knobs, model catalog) |
| `PUT /api/settings` | superadmin | Validate and replace the document; omitted fields fall back to defaults, the models.dev sync status is server-owned and preserved. Validation failures return 400 and change nothing. Applies in-request — pool mode and the `/v1/models` cache refresh before the response |
| `POST /api/settings/model-meta/sync` | superadmin | Run the models.dev catalog sync now; returns the sync status (200 even when `ok:false` — the error is in the status block) |
| `GET /api/dashboard` | user | Pool health, per-key counters, recent activity |
| `GET /api/usage/summary?from=&to=` | user | Requests/errors/tokens totals, top models, per-provider (scoped to own user; superadmin sees all) |
| `GET /api/usage/keys?from=&to=` | user | Per-client-key requests/tokens in range (scoped) |
| `GET /api/usage/users?from=&to=` | superadmin | Per-user requests/tokens in range |
| `GET /api/usage/activity?limit=` | user | Persisted recent requests with token counts (scoped) |
| `POST /api/me/password` | user | Self-service password reset; requires the current password and wipes all sessions |

Every mutation writes to SQLite and rebuilds the in-memory pools/key cache
in the same request. Usage ranges are unix seconds, capped to 92 days;
token counts come from whatever the upstream reports (0 when it reports
none). Events persist in the `usage_events` table and are pruned after 90
days.

## Errors

| Status | Meaning |
| --- | --- |
| 400 | Unknown provider prefix, or malformed request |
| 401 | Missing/invalid client key |
| 502 | All keys cooling/disabled ("no healthy keys"), or an upstream client-shape/version rejection — the body carries an actionable hint (e.g. raise the Zen user agent in Settings) |

Failover across keys happens until the first response byte reaches the
client; after that, upstream failures propagate rather than corrupting a
stream mid-flight.
