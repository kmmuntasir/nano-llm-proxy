# Configuration

`nano-llm-proxy` has two configuration layers, deliberately split:

1. **Bootstrap** — the handful of values needed before the database opens.
   Environment variables, optionally seeded from a `.env` file in the working
   directory. There is no config file.
2. **Runtime** — everything you tune while it runs (rotation, retries, the
   Claude fallback, adapter knobs, the model catalog). A single JSON document in the
   SQLite `settings` table, edited in the admin GUI under **Settings** (or via
   `PUT /api/settings`). Every change applies in-request — no restarts.

## Bootstrap (environment)

`.env` is parsed by the binary itself: `KEY=VALUE` per line, `#` comments,
optional quotes, and it **never overrides variables already present in the
real environment**. Precedence: built-in defaults < `.env` < process
environment (so `systemd EnvironmentFile=` and `docker -e` work unchanged).

| Variable | Default | Meaning |
| --- | --- | --- |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | — | First superadmin's credentials. Required at first boot (bootstrap fails fast without them), create-if-absent on later boots; also used by `-reset-admin-password` |
| `NANO_PORT` | 8787 | HTTP listen port (1–65535) |
| `NANO_BIND` | `127.0.0.1` | Listen address. Use `0.0.0.0` in a VM |
| `NANO_DB_PATH` | `gateway.db` | SQLite database (created and migrated at boot, 0600) |
| `NANO_COOKIE_SECURE` | true | `Secure` flag on GUI session cookies. Set false only for plain-HTTP local use |
| `NANO_TRUSTED_ORIGINS` | empty | Comma-separated extra Origin hosts accepted on state-changing `/api` requests. Same-host is always allowed. Set your public host here when serving the GUI behind a reverse proxy |

Malformed values are fatal at boot (`invalid NANO_PORT "…"`), never silently
ignored. See `.env.example` for a commented template.

## Runtime settings (database + GUI)

The document lives in the `settings` table under the key `runtime_settings`.
A missing row means "all defaults". `GET /api/settings` returns the effective
document; `PUT /api/settings` validates and replaces it (omitted fields fall
back to their defaults, explicit `false`/`0` are honored; the sync-status
block is server-owned).

| Path | Default | Meaning |
| --- | --- | --- |
| `rotation` | `priority` | `priority` (stick with failover, prompt-cache friendly) or `lru` (spread load). Rebuilds every pool on save |
| `retry.maxKeysPerRequest` | 3 | Failover attempts per request (1–100) |
| `retry.cooldownSeconds` | 30 | Default 429 cooldown (0 = only honor `Retry-After`) |
| `retry.respectRetryAfter` | true | Prefer the upstream `Retry-After` duration |
| `retry.maxRequestsPerKeyPerDay` | 0 (off) | Optional per-key daily cap; an exhausted key cools until midnight |
| `anthropic.fallbackModel` | empty | Target for literal `claude-*` requests (background tasks clients self-issue) on `/v1/messages`; empty = pass through untouched |
| `zen.userAgent` | `opencode/1.18.32` | Client user agent; must satisfy the upstream's 1.18.0 floor. Validated on save (HTTP 400) and at boot (fatal) |
| `zen.injectTools` | true | Append the client-shape tool stubs to every zen request |
| `zen.responsesModels` | — | *Removed.* Responses support is a per-model flag on `modelMeta` entries; toggle it per model on the Providers page (zen models). Sync preserves the flag across catalog refreshes |
| `zen.freeOnly` | false | Restrict the zen catalog in `/v1/models` to models flagged as free upstream |
| `zen.modelMeta` | empty | Per-model metadata (context window, max output, modalities, reasoning, description) shown in `/v1/models`; unknown models get conservative defaults |
| `zen.modelMetaAutoSync` | false | Refresh `modelMeta` from models.dev once a day in the background |
| `kilo.freeOnly` | false | Same catalog restriction as `zen.freeOnly`, applied to Kilo preset providers |
| `zai.modelMeta` | empty | Per-model metadata for Z.ai preset providers (context window, max output, modalities, reasoning, description); filled by the models.dev sync. Unknown models advertise a 1M context window so coding agents don't downshift to 128K |
| `webTools.enabled` | false | Serve the MCP web tools at `POST /mcp` to every client key. Off after upgrades — the SearXNG/obscura backends must be installed first (see `docs/deployment.md`) |
| `webTools.searxngUrl` | `http://127.0.0.1:8888` | SearXNG base URL; the JSON API must be enabled there (`search.formats` includes `json`) |
| `webTools.readerMode` | `fast` | `fast` = native fetch first, escalate to obscura; `render` = obscura first, fall back to native. Either way `web_read`'s `render: true` argument forces the browser leg |
| `webTools.obscuraPath` | `obscura` | obscura binary — bare name (resolved on the service's PATH) or absolute path |
| `webTools.obscuraStealth` | false | Pass `--stealth` to obscura (anti-bot fingerprinting) |
| `webTools.obscuraConcurrency` | 2 | Max concurrent obscura processes (1–8); each is capped to a 128 MB V8 heap |
| `webTools.fetchTimeoutSeconds` | 15 | Native fetch leg timeout (1–120) |
| `webTools.obscuraTimeoutSeconds` | 30 | obscura process timeout (5–300) |
| `webTools.maxChars` | 20000 | Default clamp on `web_read` output; per-call `maxChars` can lower it (1000–200000) |

Ranges enforced on save: `maxKeysPerRequest` 1–100, `cooldownSeconds`
0–86400, `maxRequestsPerKeyPerDay` 0 or 1–1000000, `fallbackModel` shaped
`provider/model`, every `modelMeta` limit a positive integer,
`webTools.obscuraConcurrency` 1–8, `webTools.fetchTimeoutSeconds` 1–120,
`webTools.obscuraTimeoutSeconds` 5–300, `webTools.maxChars` 1000–200000,
`webTools.searxngUrl` an http(s) URL with a host, `webTools.obscuraPath`
non-empty without whitespace.

Base URLs are **not** part of this document — providers (and their base
URLs) live in their own database table and are edited on the GUI Providers
page. Only `zen` is built in; every other provider is added from the Add
Provider dropdown — a curated preset, or its Custom Provider option for
hand-typed endpoints.

## Model catalog sync (models.dev)

Zen's and Z.ai's `/models` endpoints advertise ids only, so the context
windows, output limits, reasoning flags, and descriptions shown in
`/v1/models` come from [`models.dev`](https://models.dev) — the model
directory the opencode ecosystem publishes. One sync refreshes both catalogs
(Settings → Model catalog → *Sync now*, or daily when `modelMetaAutoSync` is
on). It fetches `https://models.dev/api.json` and then:

- keeps the `opencode` provider's models for `zen.modelMeta`: overwrites the
  meta of live zen ids models.dev knows (manual curation of those ids is
  overwritten), leaves manual entries for unknown ids untouched, and prunes
  meta whose model no longer appears in zen's live catalog (a re-added model
  comes back on the next sync),
- merges the `zai-coding-plan`, `zhipuai-coding-plan`, `zai`, and `zhipuai`
  entries into `zai.modelMeta`, first hit winning per model id — the
  coding-plan entries describe exactly what the coding endpoint serves and
  the platform entries fill in older models; ids that vanish from all four
  entries are pruned, and models the catalog doesn't know yet fall back to a
  1M context window in `/v1/models`,
- never touches the per-model `responsesApi` flags,
- on any fetch/parse/persist failure changes nothing and records the error in
  the sync status shown in the GUI.

## CLI

```text
nano-llm-proxy [keys.json] [-reset-admin-password]
```

- `keys.json` is a legacy first-boot seeding source
  (`{"zen":[{"label","key"}...],"kilo":[...]}`). Only the `zen` entries are
  imported now — kilo keys are no longer auto-seeded (kilo is a preset added
  from the GUI; existing databases keep their kilo keys through the
  migration). Once a database exists the file is ignored — new deployments
  manage upstream keys in the GUI.
- `-reset-admin-password` resets the superadmin's password from
  `ADMIN_PASSWORD` and exits; restart the service afterwards.

## Resetting settings

To discard every runtime setting and return to defaults:

```bash
sqlite3 gateway.db "DELETE FROM settings WHERE key='runtime_settings'"
```

then restart. The same command is the escape hatch if a hand-edited user
agent ever blocks boot.

## Deployment

### The short way

On a systemd host (any 512 MB VPS will do):

```bash
cp .env.example .env      # fill in ADMIN_EMAIL / ADMIN_PASSWORD
sudo ./deploy.sh
```

The script builds the GUI and binary, installs to `/opt/nano-llm-proxy`,
creates an unprivileged service user, installs a hardened unit, and starts
the service. Re-running it upgrades in place.

### systemd, hand-rolled

Build on any machine (Go 1.26+ and, for the GUI, Node 22+):

```bash
(cd web && npm ci && npm run build)
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .
scp nano-llm-proxy root@server:/usr/local/bin/
```

`/etc/nano-llm-proxy/nano.env` (chmod 600):

```text
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=...
```

`/etc/systemd/system/nano-llm-proxy.service`:

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
SQLite write fail. Logs go to journald: `journalctl -u nano-llm-proxy -f`.

### Behind a reverse proxy

The GUI needs one of two things to accept state-changing requests:

- the browser's `Origin` host equals the backend `Host` (typical for Caddy
  and correctly configured nginx), or
- the host is listed in `NANO_TRUSTED_ORIGINS`.

nginx snippet (streaming-safe):

```nginx
location / {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_buffering off;
    proxy_read_timeout 3600s;
}
```

Caddy needs nothing beyond `reverse_proxy 127.0.0.1:8787`.

Serve over TLS and keep `NANO_COOKIE_SECURE=true` in production.

## Operations

- **Choose the primary key:** in `priority` rotation the first healthy key of
  a provider serves everything. Keys run in the order they were added to the
  provider; disable the busy one in the GUI to push traffic down the pool.
- **Re-enable / disable an upstream key:** GUI → Providers → key switch;
  effective immediately (pools rebuild in-request).
- **Cooldowns, daily-cap state, and pool counters are in-memory:** a restart
  clears them. Users, keys, providers, and settings persist in SQLite;
  startup takes about two seconds.
- **Health:** `GET /health` is open and reports uptime and per-provider
  healthy-key counts.
