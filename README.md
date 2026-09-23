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
| Provider quirks (API surface splits, client-shape checks) | Built-in adapters normalize the odd ones; the generic adapter covers any OpenAI-compatible URL |
| Admin usually means YAML edits + restarts | Embedded web GUI: users, client keys, providers, upstream keys, live dashboard |

## Features

- Single static binary (~10 MB, pure Go, no cgo); idles around 15 MB of RAM
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
  upstream-key management, live dashboard with a recent-activity ring
- SQLite (WAL) is the source of truth; memory is the hot path — every GUI
  mutation rebuilds the pools in the same request
- Self-describing model catalog: entries carry context window, max output,
  input modalities, and reasoning flags; model IDs embed the same facts
- Claude Code support: model aliases, `[1m]` context-suffix handling, and a
  documented `ANTHROPIC_DEFAULT_*_MODEL` integration
- 31 tests (`go test -race ./...`) against scripted mock upstreams — no
  network or Node required

## Quickstart (from source)

Prerequisites: Go 1.26+ and Node 22+.

```bash
git clone https://github.com/kmmuntasir/nano-llm-proxy.git
cd nano-llm-proxy
(cd web && npm ci && npm run build)   # builds the GUI; it gets embedded into the binary
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .
cp config.example.json config.json    # sane local defaults (plain-HTTP cookies)
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=change-me ./nano-llm-proxy
```

Then:

1. Open `http://localhost:8787` and log in with those credentials.
2. **Providers** → add a provider: a name (it becomes the model prefix, e.g.
   `openai`), a base URL (e.g. `https://api.openai.com/v1`), and one or more
   API keys.
3. **My Keys** → create a client key (`fg-…`).
4. Talk to it:

```bash
curl -N http://localhost:8787/v1/chat/completions \
  -H "Authorization: Bearer fg-..." \
  -H "Content-Type: application/json" \
  -d '{"model":"openai/gpt-4o-mini","stream":true,
       "messages":[{"role":"user","content":"hello"}]}'
```

No `config.json`? Fine — the gateway runs on built-in defaults (port 8787,
loopback bind, `gateway.db` in the working directory). The file only
overrides.

### Docker

```bash
docker build -t nano-llm-proxy .
docker run -d --name nano-llm-proxy -p 8787:8787 \
  -v nano-llm-proxy-data:/data \
  -e ADMIN_EMAIL=admin@example.com -e ADMIN_PASSWORD=change-me \
  nano-llm-proxy
```

## Model routing

Requests pick a provider by model prefix; the prefix (and any metadata
suffix) is stripped before the upstream call:

```text
openai/gpt-4o-mini        → provider "openai",  model "gpt-4o-mini"
groq/llama-3.3-70b        → provider "groq",    model "llama-3.3-70b"
<adapter>/<model>         → built-in adapters (zen, kilo)
```

`GET /v1/models` merges every enabled provider's catalog and enriches each
entry with `context_window`, `max_output_tokens`, modalities, `reasoning`,
and `responses_api`. Where metadata is available, IDs are advertised in a
self-describing form so model pickers can show the facts:

```text
<provider>/<model>-<context>(-txt|-img|-vid|-aud|-pdf)*

myprovider/my-model-128K-txt-img     ← 128K context, text+image input
myprovider/my-model-1M               ← 1M context, no modality data
```

The suffix is cosmetic — every endpoint strips it before calling upstream,
and bare IDs are accepted everywhere.

## Key rotation

Each provider's keys form an ordered pool. Two modes (config `rotation`):

| Mode | Behavior | Good for |
| --- | --- | --- |
| `priority` (default) | Traffic sticks to the first healthy key; failover walks down the list | Prompt-cache affinity — agentic sessions re-send long transcripts every turn |
| `lru` | Every request takes the least-recently-used key | Spreading load across many keys or many users |

Failure handling per request:

| Upstream response | Key action | Request action |
| --- | --- | --- |
| 429 with `Retry-After` | cooldown for that duration | next key |
| 429 without | 30 s cooldown | next key |
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

Optional `retry.maxRequestsPerKeyPerDay` caps per-key daily use (an exhausted
key cools until midnight); off by default.

## Built-in provider adapters

| Adapter | Upstream | What it handles |
| --- | --- | --- |
| `zen` | OpenCode Zen (`opencode.ai/zen/v1`) | Injects the client headers and tool stubs the API requires, routes each model to its Chat or Responses surface (self-correcting on the signature 503), and translates between the two shapes — including tool calls |
| `kilo` | Kilo Code (`api.kilo.ai/api/gateway/v1`) | OpenAI-compatible passthrough with rotation |
| generic | any OpenAI-compatible base URL | GUI-added providers (OpenAI, Groq, Together, vLLM, Ollama, …) — no code needed |

Adapter-specific settings live under the `zen` and `kilo` config keys — see
[docs/configuration.md](docs/configuration.md).

## Using Claude Code

Claude Code enforces a known-model catalog client-side and lets
`~/.claude/settings.json` `env` override the process environment — so the
integration uses env slots plus the gateway's model aliases:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_AUTH_TOKEN": "fg-...",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "openai/gpt-4o-mini",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "groq/llama-3.3-70b-versatile",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "openai/gpt-4o-mini"
  }
}
```

- `--model opus|sonnet|haiku` substitutes the matching slot value.
- Literal `claude-*` model names hit the gateway's alias map
  (`anthropic.aliases` in the config) and get rewritten server-side.
- Append `[1m]` to a slot value to opt into 1M-context accounting; the
  gateway strips the suffix before the upstream call.

The `/v1/messages` surface speaks the full Anthropic protocol: system blocks,
`tool_use`/`tool_result`, streaming SSE events (`message_start` …
`message_stop`), stop reasons, and usage.

## Admin GUI

Served by the same binary at `/`.

| Page | Who | What |
| --- | --- | --- |
| Dashboard | everyone | Uptime, per-provider pool health, per-key status and counters, last 100 requests |
| My Keys | everyone | Create/disable/delete own client keys; plaintext shown exactly once |
| Users | superadmin | User CRUD, roles, per-user key management |
| Providers | superadmin | Add generic providers, edit base URLs, add/remove/toggle upstream keys |

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
  SQLite file — keep it at 0600 and out of version control. `config.json`,
  `keys.json`, and env files are gitignored already.

## Configuration

The config file is optional; every field has a default (see
`config.example.json`). Full reference in
[docs/configuration.md](docs/configuration.md).

| Field | Default | Meaning |
| --- | --- | --- |
| `port` / `bind` | 8787 / 127.0.0.1 | Listen address |
| `dbPath` | `gateway.db` | SQLite database |
| `cookieSecure` | true | Secure flag on session cookies (false for plain HTTP) |
| `trustedOrigins` | empty | Extra Origins allowed on GUI mutations behind a reverse proxy |
| `rotation` | `priority` | `priority` or `lru` |
| `retry.*` | 3 / 30 s / true | Failover depth, cooldown, honor `Retry-After` |
| `anthropic.aliases` | empty | `claude-*` → model rewrites on `/v1/messages` |
| `zen.*` / `kilo.*` | adapter defaults | Built-in adapter settings |

## Deploying

A systemd unit (full walkthrough in
[docs/configuration.md](docs/configuration.md)):

```ini
[Unit]
Description=nano-llm-proxy
After=network-online.target

[Service]
DynamicUser=yes
StateDirectory=nano-llm-proxy
WorkingDirectory=/var/lib/nano-llm-proxy
EnvironmentFile=/etc/nano-llm-proxy/gateway.env
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
| `main.go` | wiring, route table, `/v1/models` catalog, `/health`, SPA serving |
| `gateway.go` | gateway assembly, hot-path rebuild, chat entry, failure classifier |
| `pool.go` | key state machine, priority/LRU selection, cooldowns, session forging |
| `zen.go` | zen adapter: client-shape injection, surface map, chat → Responses requests |
| `responses.go`, `responses_endpoint.go` | Responses ↔ chat translation; native `/v1/responses` |
| `anthropic.go`, `anthropic_handler.go` | Anthropic Messages conversion, aliases, `[1m]` handling |
| `generic.go` | generic OpenAI-compatible proxy |
| `store.go` | SQLite: migrations, bootstrap, users/keys/providers CRUD |
| `auth.go` | sessions, bcrypt, roles, Origin checks, login backoff |
| `api_*.go` | `/api` JSON handlers |
| `clientkeys.go` | client-key cache + usage tracker |
| `modelid.go`, `sse.go`, `config.go` | ID suffix grammar, streaming helpers, config schema |
| `web/` | React 19 + Chakra UI admin GUI (Vite, TypeScript) |

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| 401 on `/v1/*` | missing or wrong client key | create one in the GUI (My Keys) |
| 502 with a client-shape hint | provider rejected the adapted request | check the adapter's config (`userAgent`, injection flags) |
| 502 "no healthy keys" | all keys cooling or disabled | check Dashboard → Providers; re-enable or wait out cooldowns |
| GUI login loop over plain HTTP | `cookieSecure: true` without TLS | set it false locally, or serve over HTTPS |
| GUI mutations 403 behind a reverse proxy | browser Origin differs from backend Host | add your public host to `trustedOrigins` |
| Empty model replies, `finish_reason: "length"` | output budget consumed by hidden reasoning | raise `max_tokens` (≥ 500) |
| Claude Code: "Unknown Model" before any request | CC validates model names client-side | use `ANTHROPIC_DEFAULT_*_MODEL` slots or a `claude-*` alias |

## Responsible use

The gateway routes traffic through whatever upstream accounts you configure —
and those accounts' terms of service still apply to everything it sends. Use
keys you are authorized to use, keep an eye on the per-key counters in the
dashboard, and keep volumes within what the accounts are meant for.

## License

GPL-3.0-or-later — see [LICENSE](LICENSE).
