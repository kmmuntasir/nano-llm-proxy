# nano-llm-proxy

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)

A tiny, single-binary LLM gateway. Pool several upstream providers and their
API keys behind one endpoint that speaks **OpenAI chat**, **OpenAI
Responses**, and **Anthropic Messages** — with health-tracking key rotation,
an embedded admin GUI, and no runtime dependencies beyond a single SQLite
file.

Think "personal API gateway for language models": point your scripts, IDE
extensions, and CLI agents at one URL with one key, while the gateway rotates
a pool of upstream keys, survives rate limits and flaky upstreams, and shows
you what happened in a web UI. A lightweight alternative to dragging a Python
control plane around just to front a few API keys.

## Why

| Problem | What nano-llm-proxy does |
| --- | --- |
| Keys scattered across several providers | One endpoint, one client key; upstream keys never leave the server |
| Per-key rate limits interrupt long agent sessions | Requests fail over across the pool: cooldowns, `Retry-After`, health tracking |
| Clients speak different protocols | The same pool serves OpenAI chat, OpenAI Responses, and Anthropic Messages |
| Provider quirks (API surface splits, client-shape checks) | The built-in zen adapter normalizes the odd ones; the generic adapter covers any OpenAI-compatible URL |
| Admin usually means YAML edits + restarts | Embedded web GUI: users, client keys, providers, upstream keys, usage charts, live dashboard |

## Features

- Single static binary (~12 MB, pure Go, no cgo); idles around 15 MB of RAM
- Three request surfaces — `/v1/chat/completions`, `/v1/responses`,
  `/v1/messages` — plus a merged `/v1/models` catalog and an open `/health`
- Streaming everywhere; non-streaming clients get aggregated responses
- Key pools per provider: `priority` rotation (stick-with-failover,
  prompt-cache friendly) or `lru` (spread); cooldowns honor `Retry-After`
- Failure classification: genuine auth failures disable a key; rate limits
  and upstream flakes cool it down briefly; client-shape problems fail fast
  with an actionable hint
- Embedded admin GUI (React, served by the same binary): users and roles,
  per-user client keys (stored hashed, plaintext shown once), provider and
  upstream-key management, self-service profiles, usage dashboards with
  charts, and a live recent-activity feed
- SQLite (WAL) is the source of truth; memory is the hot path — every GUI
  mutation rebuilds the pools in the same request
- Self-describing model catalog: entries carry context window, max output,
  input modalities, and reasoning flags; model IDs embed the same facts
- Claude Code support: a configurable `claude-*` fallback model,
  `[1m]` context-suffix handling, and a documented
  `ANTHROPIC_DEFAULT_*_MODEL` integration
- Usage monitoring: per-request token capture, per-user/key/model/provider
  aggregations over date ranges (90-day retention), persisted recent activity
- Runtime settings live in the database and are editable in the GUI with no
  restart: rotation mode, retry/cooldown knobs, an optional per-key daily cap,
  a Claude `claude-*` fallback model, adapter knobs, and models.dev-backed
  Zen and Z.ai model catalogs that sync themselves
- Self-hosted MCP web tools at `/mcp`: `web_search` (SearXNG) and `web_read`
  (native fetch + obscura headless-browser rendering) with SSRF protection,
  per-user metering, and an idempotent installer (`scripts/install-web-tools.sh`)
  — no paid search APIs
- 141 tests (`go test -race ./...`) against scripted mock upstreams and
  local fixture servers — no network or Node required

## Quickstart (from source)

Prerequisites: Go 1.26+ and Node 22+.

```bash
git clone https://github.com/kmmuntasir/nano-llm-proxy.git
cd nano-llm-proxy
(cd web && npm ci && npm run build)   # builds the GUI; it gets embedded into the binary
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .
cp .env.example .env                  # then set ADMIN_EMAIL / ADMIN_PASSWORD
./nano-llm-proxy
```

Then:

1. Open `http://localhost:8787` and log in with those credentials.
2. **Providers** → add a provider: a name (it becomes the model prefix, e.g.
   `openai`), an endpoint root — OpenAI-compatible (e.g.
   `https://api.openai.com/v1`), Anthropic-compatible (e.g.
   `https://api.z.ai/api/anthropic`), or both — and one or more API keys.
3. **Profile → My API Keys** → create a client key (`fg-…`).
4. Talk to it:

```bash
curl -N http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer fg-..." \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","stream":true,
       "messages":[{"role":"user","content":"hello"}]}'
```
No `.env`? That works too — the gateway runs on built-in defaults (port
8787, loopback bind, `gateway.db` in the working directory); only the
first-boot superadmin credentials are mandatory.

## Model routing

Requests pick a provider by model prefix; the prefix (and any metadata
suffix) is stripped before the upstream call:

```text
openai/gpt-4o-mini        → provider "openai",  model "gpt-4o-mini"
groq/llama-3.3-70b        → provider "groq",    model "llama-3.3-70b"
<adapter>/<model>         → the built-in zen adapter, or preset providers (kilo, zai, …)
```

`GET /v1/models` merges every enabled provider's catalog and enriches each
entry with `context_window`, `max_output_tokens`, modalities, `reasoning`,
and `responses_api`. The catalog is scoped per client key when a superadmin
has revoked providers for the key's owner (Users → Provider access):
revoked providers vanish from the list and route like unknown prefixes.
Where metadata is available, IDs are advertised in a
self-describing form so model pickers can show the facts:

```text
<provider>/<model>-<context>(-txt|-img|-vid|-aud|-pdf)*

myprovider/my-model-128K-txt-img     ← 128K context, text+image input
myprovider/my-model-1M               ← 1M context, no modality data
```

The suffix is cosmetic — every endpoint strips it before calling upstream,
and bare IDs are accepted everywhere.

## Key rotation

Each provider's keys form an ordered pool. Two modes (Settings → Routing):

| Mode | Behavior | Good for |
| --- | --- | --- |
| `priority` (default) | Traffic sticks to the first healthy key; failover walks down the list | Prompt-cache affinity — agentic sessions re-send long transcripts every turn |
| `lru` | Every request takes the least-recently-used key | Spreading load across many keys or many users |

Failure handling per request:

| Upstream response | Key action | Request action |
| --- | --- | --- |
| 429 with `Retry-After` | cooldown for that duration | next key |
| 429 without | 30 s cooldown — Z.ai coding-plan 429s state the reset in the body, and the key cools until that instant | next key |
| 401 genuine auth error | disabled until re-enabled | next key |
| 401 anything else | 10 s cooldown (some providers wrap flakes as 401) | next key |
| 403 client-shape rejection | none — not the key's fault | fail fast with a hint |
| 426 upgrade required | none | fail fast: raise the adapter's `userAgent` |
| 503 endpoint unavailable | none | flip Chat/Responses surface, retry |
| other 4xx | none | fail fast, upstream body surfaced |
| 5xx / network error | 10 s cooldown | next key |

Two structural rules:

- Failover happens only until the first response byte reaches the client;
  after that a mid-stream failure propagates instead of silently restarting
  the stream.
- Cooldowns are timestamps, not counters. When the primary key's cooldown
  lapses it is simply first-healthy again — one probe request per cycle,
  self-correcting.

An optional daily cap per key (Settings → Retries) cools an exhausted key
until midnight; off by default.

## Provider adapters

One adapter is built in; everything else — including the Kilo preset — is a
GUI-added provider from the preset registry (see [Provider
presets](#provider-presets)), served by the generic passthrough:

| Adapter | Upstream | What it handles |
| --- | --- | --- |
| `zen` (built in) | OpenCode Zen (`opencode.ai/zen/v1`) | Injects the client headers and tool stubs the API requires, routes each model to its Chat or Responses surface (self-correcting on the signature 503), and translates between the two shapes — including tool calls |
| presets (Kilo, DeepSeek, Z.ai, …) | per preset | OpenAI/Anthropic passthrough with rotation; presets with special catalogs map their metadata (Kilo's rich catalog, Z.ai's models.dev merge) |
| generic | any OpenAI- and/or Anthropic-compatible base URL | GUI-added providers — no code needed |

Adapter-specific settings (user agent, fingerprint injection, free-only
filters, the Zen model catalog) live in the GUI under Settings — see
[docs/configuration.md](docs/configuration.md).

## Provider presets

The GUI's **Add Provider** card offers a curated dropdown so a new upstream
is one click plus an API key — no URL typing:

| Preset | OpenAI root | Anthropic root |
| --- | --- | --- |
| Anthropic | — | `api.anthropic.com` |
| DeepSeek | `api.deepseek.com/v1` | `api.deepseek.com/anthropic` |
| Groq | `api.groq.com/openai/v1` | — |
| Kilo | `api.kilo.ai/api/gateway/v1` | — |
| Kimi (Moonshot) | `api.moonshot.ai/v1` | `api.moonshot.ai/anthropic` |
| MiniMax | `api.minimax.io/v1` | `api.minimax.io/anthropic` |
| OpenAI | `api.openai.com/v1` | — |
| OpenRouter | `openrouter.ai/api/v1` | `openrouter.ai/api` |
| xAI (Grok) | `api.x.ai/v1` | — |
| Z.ai (GLM Coding Plan) | `api.z.ai/api/coding/paas/v4` | `api.z.ai/api/anthropic` |

Endpoints are release-managed (read-only in the GUI); anything unusual — a
self-hosted gateway, a custom deployment — goes through the same dropdown's
**Custom Provider…** option, which reveals the endpoint inputs instead. The
preset id is stored on the provider row, which is
the hook for future per-provider behavior. Presets with special catalogs
carry their own enrichment logic in `internal/gateway/providerspec.go` —
Kilo maps its rich upstream metadata, and Z.ai merges four models.dev
entries into a synced catalog (models it doesn't know yet advertise a 1M
context window so agents don't downshift to 128K); `GET
/api/providers/presets` lists the registry. Adding the same preset twice is
fine (each gets its own name and keys).

## Dual-endpoint providers

A GUI-added provider can carry both endpoint roots, and either alone is
valid — at least one is required:

- OpenAI root (`baseUrl`): serves `/v1/chat/completions` and the catalog.
  Requests pass through as-is — no translation.
- Anthropic root (`anthropicBaseUrl`): `/v1/messages` is forwarded
  natively. The body, `anthropic-beta` headers, and SSE event framing go
  upstream untouched — thinking blocks with signatures, interleaved
  `system` messages, and the coding-agent fingerprint all survive; only
  the credential is swapped for a pooled upstream key.
- Anthropic-only providers still serve OpenAI clients: `/v1/chat/completions`
  is translated to Messages on the way out (system messages, tools,
  tool_calls, streamed `reasoning_content`) and the response back.
- The model catalog prefers the OpenAI root and falls back to the
  Anthropic root's `/v1/models` when that fails.

Z.ai's GLM Coding Plan is the canonical case: one provider, two roots —
`https://api.z.ai/api/coding/paas/v4` (OpenAI) and
`https://api.z.ai/api/anthropic` (Anthropic).

## Using Claude Code

Claude Code enforces a known-model catalog client-side and lets
`~/.claude/settings.json` `env` override the process environment — so the
integration uses env slots for your main models plus the gateway's
`claude-*` fallback for everything else:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_AUTH_TOKEN": "fg-...",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "zai/glm-5.3[1m]",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "zai/glm-5.3-flash",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "zen/mimo-v2.6-flash-free"
  }
}
```

- `--model opus|sonnet|haiku` substitutes the matching slot value.
- Literal `claude-*` model names (background tasks Claude Code self-issues)
  hit the gateway's fallback model (Settings → Anthropic fallback) and get
  rewritten server-side.
- Append `[1m]` to a slot value to opt into 1M-context accounting; the
  gateway strips the suffix before the upstream call.

The `/v1/messages` surface speaks the full Anthropic protocol: system blocks,
`tool_use`/`tool_result`, streaming SSE events (`message_start` …
`message_stop`), stop reasons, and usage.

## MCP web tools

The gateway embeds an MCP server at `POST /mcp` serving two self-hosted
tools to every client key — `web_search` (SearXNG) and `web_read` (fast
native fetch escalating to the obscura headless browser for
JavaScript-heavy or bot-protected pages). SSRF-protected, metered per user
on the Usage page, no paid search APIs. The signed-in **Guide** page in the
GUI generates setup snippets for Claude Code, opencode, Codex CLI, Kilo
Code, and plain MCP clients with the deployment's own URLs. The backends
are optional companion services installed by an idempotent script — see
[docs/deployment.md](docs/deployment.md).

```bash
claude mcp add -s user -t http nano-web https://your-gateway/mcp \
  --header "Authorization: Bearer fg-..."
```

## Admin GUI

Served by the same binary at `/`.

| Page | Who | What |
| --- | --- | --- |
| Dashboard | everyone | Uptime, 24h requests/tokens/errors, usage charts (Today / 7d / 30d: requests, tokens, providers, top models), recent requests |
| Models | everyone | Every live model across providers — the gateway's base URL with a one-click Claude Code `settings.json` env generator (pick opus/sonnet/haiku, copy the env object; `[1m]` is added automatically for 1M-context models) — plus search and filters by provider, context window, reasoning, Responses API, and input modalities; every model shows its exact ID with a copy button |
| Guide | everyone | User guide rendered with this deployment's URLs: base endpoints, setup for Claude Code / opencode / Codex CLI / Kilo Code / generic OpenAI-compatible clients, MCP web-tools setup, troubleshooting |
| Usage | everyone | Date-range usage: totals, top models, providers, per-key (admins also get per-user), recent activity — scoped to the signed-in user |
| Profile | everyone | Account info, self-service password reset, own client keys (create/disable/delete) plus an MCP web-tools setup dialog |
| Users | superadmin | User CRUD, roles, password resets, per-user key management, per-user provider access (revoke a provider and it vanishes from that user's model list; their requests to it fail like an unknown provider) |
| Providers | superadmin | Add providers from a curated preset dropdown (or its Custom Provider option), search/filter the provider card grid, edit base URLs, add/remove/toggle/rename upstream keys, fetch per-key plan usage for Z.ai (5-hour/weekly windows, tier, resets), browse each provider's live model catalog (zen models with the Responses-API flag carry a toggle) |
| Settings | superadmin | Rotation, retries/cooldowns, daily cap, Claude fallback, adapter knobs, model-catalog sync (zen + zai, models.dev), web tools (SearXNG URL, reader mode, obscura knobs, live backend tests) |

First boot requires `ADMIN_EMAIL` and `ADMIN_PASSWORD` (environment variables
or an env file); the superadmin is created once and never overwritten.
Forgot the password? Run `./nano-llm-proxy -reset-admin-password` with
`ADMIN_PASSWORD` set, then restart.

## Security model

- Clients authenticate with `fg-…` keys: stored sha256-hashed, cached in
  memory, and revocation takes effect on the very next request.
- GUI logins: bcrypt passwords, HttpOnly session cookies, per-IP+email login
  backoff, Origin checks on state-changing requests.
- Upstream provider keys are visible only to the server (the GUI shows hashed
  prefixes and status). They must remain usable verbatim, so they live in the
  SQLite file — keep it at 0600 and out of version control. `keys.json` and
  `.env` files are gitignored already.

## Configuration

Two layers, deliberately split:

**Bootstrap** — the few values needed before the database opens. Environment
variables, optionally seeded from a `.env` file in the working directory (the
real environment always wins; see `.env.example`). Full reference in
[docs/configuration.md](docs/configuration.md).

| Variable | Default | Meaning |
| --- | --- | --- |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | — | First-boot superadmin credentials (required once) |
| `NANO_PORT` / `NANO_BIND` | 8787 / 127.0.0.1 | Listen address |
| `NANO_DB_PATH` | `gateway.db` | SQLite database |
| `NANO_COOKIE_SECURE` | true | Secure flag on session cookies (false for plain HTTP) |
| `NANO_TRUSTED_ORIGINS` | empty | Extra Origins allowed on GUI mutations behind a reverse proxy |

**Runtime** — everything you tune while it runs (rotation, retries, daily
cap, the Claude fallback, adapter knobs, the model catalogs, web tools)
lives in the database and is edited in the GUI under **Settings**. Changes
apply in-request; no restarts.

## Deploying

`sudo ./deploy.sh` does the whole setup on a systemd host: it requires a
`.env`, builds GUI + binary, installs to `/opt/nano-llm-proxy`, and installs
a hardened unit. Optional MCP web-tool backends (SearXNG + obscura) are a
separate, idempotent installer — `sudo ./scripts/install-web-tools.sh` —
documented in [docs/deployment.md](docs/deployment.md). Hand-rolled
equivalent (full walkthrough in
[docs/configuration.md](docs/configuration.md)):

```ini
[Unit]
Description=nano-llm-proxy
After=network-online.target

[Service]
DynamicUser=yes
StateDirectory=nano-llm-proxy
WorkingDirectory=/var/lib/nano-llm-proxy
EnvironmentFile=/etc/nano-llm-proxy/nano.env
ExecStart=/usr/local/bin/nano-llm-proxy
Restart=on-failure
ProtectSystem=strict
ReadWritePaths=/var/lib/nano-llm-proxy

[Install]
WantedBy=multi-user.target
```

`ReadWritePaths` matters: without it, `ProtectSystem=strict` makes every
SQLite write fail.

## Development

```bash
go vet ./... && go test -race ./...   # backend; no network, no Node needed
cd web && npm ci && npm run dev       # GUI dev server
```

| Path | Responsibility |
| --- | --- |
| `main.go` | bootstrap wiring: .env, store open, route registration, listen |
| `internal/gateway/` | the runtime: gateway assembly, hot-path rebuild, failure classifier, key pools (priority/LRU + daily cap), zen adapter + preset/generic adapters, Responses ↔ chat and Anthropic conversions, admin API handlers, sessions/auth, models.dev sync, the `/mcp` MCP server |
| `internal/webtools/` | web-tool backends: SearXNG client, native fetch + readability → markdown, obscura executor, SSRF guard, escalation policy |
| `internal/store/` | SQLite: migrations, bootstrap, users/keys/providers/settings CRUD, usage log |
| `internal/settings/` | runtime-settings document: defaults, validation, `ModelMeta` schema |
| `internal/config/` | .env parser, env bootstrap, legacy key-file schema |
| `web/` | React 19 + Chakra UI admin GUI (Vite, TypeScript) |
| `scripts/install-web-tools.sh` | idempotent installer for the optional SearXNG + obscura companion services |

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| 401 on `/v1/*` | missing or wrong client key | create one in the GUI (Profile → My API Keys) |
| 502 with a client-shape hint | provider rejected the adapted request | check the adapter's config (`userAgent`, injection flags) |
| 502 "no healthy keys" | all keys cooling or disabled | check the Providers page; re-enable or wait out cooldowns |
| Boot refuses to start: "zen.userAgent ... fails the 1.18.0 floor" | the DB holds a too-old user agent | fix it in Settings → Zen, or reset all settings: `sqlite3 gateway.db "DELETE FROM settings WHERE key='runtime_settings'"` |
| GUI login loop over plain HTTP | `NANO_COOKIE_SECURE=true` without TLS | set it false locally, or serve over HTTPS |
| GUI mutations 403 behind a reverse proxy | browser Origin differs from backend Host | add your public host to `NANO_TRUSTED_ORIGINS` |
| Empty model replies, `finish_reason: "length"` | output budget consumed by hidden reasoning | raise `max_tokens` (≥ 500) |
| Claude Code: "Unknown Model" before any request | CC validates model names client-side | use `ANTHROPIC_DEFAULT_*_MODEL` slots; set the gateway's Claude fallback for background `claude-*` calls |
| 404 on `POST /mcp` | web tools disabled (or binary predates the feature) | enable in Settings → Web tools; install backends with `scripts/install-web-tools.sh` |
| `web_search` returns no results | search engines CAPTCHA-blocked from this IP | trim engines in `/etc/searxng/settings.yml` (see docs/deployment.md); the `duckduckgo web` engine is bot-detection-resistant |
| `web_read` returns JS-shell text | page is rendered client-side | call with `render: true`; or set reader mode `render` in Settings |

## Responsible use

The gateway routes traffic through whatever upstream accounts you configure —
and those accounts' terms of service still apply to everything it sends. Use
keys you are authorized to use, keep an eye on the per-key counters in the
dashboard, and keep volumes within what the accounts are meant for.

## License

GPL-3.0-or-later — see [LICENSE](LICENSE).
