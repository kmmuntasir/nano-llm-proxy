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
environment (so `systemd EnvironmentFile=` and exported variables work unchanged).

| Variable | Default | Meaning |
| --- | --- | --- |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | — | First superadmin's credentials. Required at first boot (bootstrap fails fast without them); they seed the superadmin only while the database has no users and are ignored after that — changing them later creates nothing. Also used by `-reset-admin-password` |
| `NANO_MODE` | `server` | `server` or `local` ([local mode](local.md): one person's computer, no sign-in). Read from the real environment only, never from `.env`. `nano-llm-proxy local` sets it too |
| `NANO_PORT` | 8787 | HTTP listen port (1–65535) |
| `NANO_BIND` | `127.0.0.1` | Listen address. Use `0.0.0.0` in a VM |
| `NANO_DB_PATH` | `gateway.db` | SQLite database (created and migrated at boot, 0600) |
| `NANO_COOKIE_SECURE` | true | `Secure` flag on GUI session cookies. Set false only for plain-HTTP local use |
| `NANO_TRUSTED_ORIGINS` | empty | Comma-separated extra Origin hosts accepted on state-changing `/api` requests. Same-host is always allowed. Set your public host here when serving the GUI behind a reverse proxy. Ignored in local mode |
| `GOOGLE_CLIENT_ID` | empty | Enables Google sign-in on the GUI login page (OAuth 2.0 *Web* client id). The GUI's public origin must be listed under the client's *Authorized JavaScript origins*. No client secret is used — the gateway verifies Google's signed ID token itself. Google sign-in only signs in users an admin added on the Users page (matched by email); it never creates accounts |
| `GOOGLE_WORKSPACE_DOMAIN` | empty | Restricts Google sign-in to one Workspace domain (bare domain, e.g. `example.com`; requires `GOOGLE_CLIENT_ID`). Set: only accounts in that domain can sign in with Google (they still need a user added on the Users page). Unset: any Google account whose email matches an existing user |

Malformed values are fatal at boot (`invalid NANO_PORT "…"`), never silently
ignored. See `.env.example` for a commented template.

### Local mode

In local mode (`nano-llm-proxy local` or `NANO_MODE=local`, see
[local.md](local.md)) the same variables apply, with these differences:

- `.env` is read from the per-user data folder
  (`nano-llm-proxy local datadir`), not the working directory.
- `NANO_DB_PATH` defaults to `gateway.db` in the data folder.
- `NANO_COOKIE_SECURE` defaults to false (the GUI is plain
  `http://localhost`; local mode has no sessions anyway).
- `NANO_BIND` must be `127.0.0.1`, `localhost` or `::1`; anything else is
  fatal at boot.
- `ADMIN_EMAIL` / `ADMIN_PASSWORD`, `GOOGLE_CLIENT_ID` and
  `GOOGLE_WORKSPACE_DOMAIN` are not used: there is no sign-in.
- The port set in Settings → This computer, or by `local install --port`, is
  written to the data folder's `.env` as `NANO_PORT` (1024–65535).

A fresh local database also starts with different runtime defaults:
`webTools.enabled` true and `webTools.searxngUrl` `http://127.0.0.1:8899`
(the installer's per-user SearXNG).

## Runtime settings (database + GUI)

The document lives in the `settings` table under the key `runtime_settings`.
A missing row means "all defaults". `GET /api/settings` returns the effective
document; `PUT /api/settings` validates and replaces it (omitted fields fall
back to their defaults, explicit `false`/`0` are honored; the sync-status
block is server-owned).

| Path | Default | Meaning |
| --- | --- | --- |
| `rotation` | `priority-daily` | `priority-daily` (stick with failover from a starting key that advances one position per day, per provider), `priority` (stick with failover from the first key, prompt-cache friendly) or `lru` (spread load). Rebuilds every pool on save |
| `retry.maxKeysPerRequest` | 3 | Failover attempts per request (1–100) |
| `retry.cooldownSeconds` | 30 | Default 429 cooldown (0 = only honor `Retry-After`) |
| `retry.respectRetryAfter` | true | Prefer the upstream `Retry-After` duration |
| `retry.maxRequestsPerKeyPerDay` | 0 (off) | Optional per-key daily cap; an exhausted key cools until midnight |
| `anthropic.fallbackModel` | empty | Target for literal `claude-*` requests (background tasks clients self-issue) on `/v1/messages`; empty = a bare `claude-*` model name is rejected (400 — it has no `provider/` prefix) |
| `zen.userAgent` | `opencode/latest/2.0.29/cli` | Client user agent, `opencode/X.Y.Z` or `opencode/<channel>/X.Y.Z/<client>`; must satisfy the upstream's 1.18.0 floor. Validated on save (HTTP 400) and at boot (fatal) |
| `zen.injectTools` | true | Append the client-shape tool stubs to every zen request |
| `zen.responsesModels` | — | *Removed.* Responses support is a per-model flag on `modelMeta` entries; toggle it per model on the Providers page (zen models). Sync preserves the flag across catalog refreshes |
| `zen.freeOnly` | false | Restrict the zen catalog in `/v1/models` to models flagged as free upstream |
| `zen.modelMeta` | empty | Per-model metadata (context window, max output, modalities, reasoning, reasoning options, description) shown in `/v1/models`; unknown models get conservative defaults |
| `zen.modelMetaAutoSync` | false | Refresh `modelMeta` from models.dev and model benchmarks from OpenRouter once a day in the background |
| `kilo.freeOnly` | false | Same catalog restriction as `zen.freeOnly`, applied to Kilo preset providers |
| `kilo.modelMeta` | empty | Per-model *reasoning* metadata (reasoning flag + reasoning options) for Kilo preset providers, filled by the models.dev sync. Kilo's own catalog already carries context/output limits, modalities and `supported_parameters`, and those always win — only the reasoning facts are taken from here. A model models.dev doesn't document keeps both fields absent rather than an unverified `false` |
| `zai.modelMeta` | empty | Per-model metadata for Z.ai preset providers (context window, max output, modalities, reasoning, reasoning options, description); filled by the models.dev sync. Unknown models advertise a 1M context window so coding agents don't downshift to 128K |
| `auth.credentialLogin` | true | Email + password sign-in on the GUI. Can be turned off only when Google sign-in is configured, and only by an admin who could sign back in with Google (inside `GOOGLE_WORKSPACE_DOMAIN` when one is set). Treated as on whenever `GOOGLE_CLIENT_ID` is unset, so removing it is the escape hatch |
| `newUsers.disabledProviderIds` | `[]` | Providers a new account with the `user` role starts without: when an admin creates such a user, a per-user revocation is written for each listed provider in the same transaction (same effect as revoking it on the Users page). Admin and superadmin accounts get none, existing users are never touched, and later role changes don't re-apply it. Ids are de-duplicated and sorted on save; ids of deleted providers are dropped on save and hidden on read. Needs no pool rebuild |
| `webTools.enabled` | false | Serve the MCP web tools at `POST /mcp` to every client key. Off after upgrades — the SearXNG/obscura backends must be installed first (see `docs/deployment.md`) |
| `webTools.searxngUrl` | `http://127.0.0.1:8888` | SearXNG base URL; the JSON API must be enabled there (`search.formats` includes `json`) |
| `webTools.readerMode` | `fast` | `fast` = native fetch first, escalate to obscura; `render` = obscura first, fall back to native. Either way `web_read`'s `render: true` argument forces the browser leg |
| `webTools.obscuraPath` | `obscura` | obscura binary — bare name (resolved on the service's PATH) or absolute path |
| `webTools.obscuraStealth` | false | Pass `--stealth` to obscura (anti-bot fingerprinting) |
| `webTools.obscuraConcurrency` | 2 | Max concurrent obscura processes (1–8); each is capped to a 128 MB V8 heap |
| `webTools.fetchTimeoutSeconds` | 15 | Native fetch leg timeout (1–120) |
| `webTools.obscuraTimeoutSeconds` | 30 | obscura process timeout (5–300) |
| `webTools.maxChars` | 20000 | Default clamp on `web_read` output; per-call `maxChars` can lower it (1000–200000) |
| `local.requireApiKey` | false | Local mode only (ignored in server mode). Off: `/v1/*` and `/mcp` accept any placeholder key or none, billed to the built-in `local (no key)` client key. On: they need a valid client key, as in server mode. Shown as **Require an API key** under Settings → This computer |

Ranges enforced on save: `maxKeysPerRequest` 1–100, `cooldownSeconds`
0–86400, `maxRequestsPerKeyPerDay` 0 or 1–1000000, `fallbackModel` shaped
`provider/model`, every `modelMeta` limit a positive integer,
`webTools.obscuraConcurrency` 1–8, `webTools.fetchTimeoutSeconds` 1–120,
`webTools.obscuraTimeoutSeconds` 5–300, `webTools.maxChars` 1000–200000,
`webTools.searxngUrl` an http(s) URL with a host, `webTools.obscuraPath`
non-empty with no control characters or surrounding spaces (spaces inside
are fine), every `newUsers.disabledProviderIds` entry a
positive integer.

Base URLs are **not** part of this document — providers (and their base
URLs) live in their own database table and are edited on the GUI Providers
page. Only `zen` is built in; every other provider is added from the Add
Provider dropdown — a curated preset, or its Custom Provider option for
hand-typed endpoints.

## Model catalog sync (models.dev)

Zen's and Z.ai's `/models` endpoints advertise ids only, so the context
windows, output limits, reasoning flags, reasoning effort ladders, and
descriptions shown in `/v1/models` come from
[`models.dev`](https://models.dev) — the model
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
- merges the `kilo` entry into `kilo.modelMeta` (only the reasoning facts —
  Kilo's own limits/modalities win in the catalog hook), keyed by Kilo's own
  ids including the `:free` suffix, which models.dev's kilo entry carries too,
- never touches the per-model `responsesApi` flags,
- carries each model's `reasoning_options` (the effort ladder agents such as
  oh-my-pi read) through into `/v1/models`; like the limits it is catalog
  data, so a refresh overwrites it,
- on any fetch/parse/persist failure changes nothing and records the error in
  the sync status shown in the GUI.

All three catalogs are sync-managed: `PUT /api/settings` replaces the whole
document, so any client that saves settings must send `zen.modelMeta`,
`zai.modelMeta` and `kilo.modelMeta` back (the admin GUI does). A save that
omits one empties it; the next sync repopulates it.

### Model benchmarks (OpenRouter)

models.dev publishes no benchmark scores. The Models page's Intelligence,
Coding and Agentic scores (Artificial Analysis indexes) and Design Arena
results come from OpenRouter's public, keyless
`https://openrouter.ai/api/v1/models`, fetched by the same *Sync now* button
and the same daily `modelMetaAutoSync` (retried hourly after a failure).

- Only models with at least one published score are kept; the snapshot is
  stored in `gateway.db` (settings row `model_benchmarks`) and survives
  restarts.
- Catalog models are matched by a normalized slug: vendor prefix, `:free` /
  `:batch` variants, zen's `-free` marker and date suffixes are ignored, and
  `.` matches `-` (`zen/glm-5.3-flash` ↔ `z-ai/glm-5.3-flash`).
- Free, stealth and very new models often have no published scores — they
  simply show none.
- A failed or empty fetch keeps the previous snapshot and reports the error.
- Scores appear on `/api/models` (the GUI) only; `/v1/models` is unchanged.

## CLI

```text
nano-llm-proxy [keys.json] [-reset-admin-password] [-version]
```

- `keys.json` is a legacy first-boot seeding source
  (`{"zen":[{"label","key"}...],"kilo":[...]}`). Only the `zen` entries are
  imported now — kilo keys are no longer auto-seeded (kilo is a preset added
  from the GUI; existing databases keep their kilo keys through the
  migration). Once a database exists the file is ignored — new deployments
  manage upstream keys in the GUI.
- `-reset-admin-password` sets the password of the account whose email is
  `ADMIN_EMAIL` to `ADMIN_PASSWORD` (both required; the password needs at
  least 10 characters), ends that account's sessions, and exits; restart the
  service afterwards.
- `-version` (or `--version`) prints the version, commit and build date and
  exits.

Local mode has its own subcommands (details in [local.md](local.md#the-local-command)):

```text
nano-llm-proxy local                      run in local mode (foreground)
nano-llm-proxy local install [--port N] [--no-autostart] [--no-open] [--quiet]
nano-llm-proxy local uninstall [--purge]
nano-llm-proxy local status
nano-llm-proxy local datadir
```

`keys.json` and `-reset-admin-password` don't apply in local mode.

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
- **Daily rotation (`priority-daily`, the default):** each provider's starting
  key is *day number mod enabled-key count*, so it moves one key down the list
  at server-local midnight and wraps from the last key to the first; failover
  continues down the list (wrapping) from there. Each provider rotates over its
  own keys. The position comes from the date alone — restarts don't reset it —
  but adding, removing, or disabling a key changes the count and therefore
  shifts which key starts today. A document saved with an explicit `rotation`
  keeps it; only fresh installs (or documents without the field) get the
  default.
- **Re-enable / disable an upstream key:** GUI → Providers → key switch;
  effective immediately (pools rebuild in-request).
- **Cooldowns, daily-cap state, pool counters, and auth-failure disables are
  in-memory:** a restart (or any GUI change, which rebuilds the pools) clears
  them — a key disabled after a genuine 401 comes back on the next rebuild;
  switch it off on the Providers page to make that permanent. Users, keys, providers, and settings persist in SQLite;
  startup takes about two seconds.
- **Health:** `GET /health` is open and reports uptime and per-provider
  healthy-key counts.
