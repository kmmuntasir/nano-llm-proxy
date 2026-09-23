# Configuration

`nano-llm-proxy` runs without a config file — every field has a default. A
`config.json` (next to the binary, or given as the first CLI argument)
overrides individual fields. Secrets come from the environment, not the file.
User accounts, client keys, providers, and upstream keys live in the SQLite
database and are managed through the admin GUI.

A minimal local config (this is `config.example.json`):

```json
{
  "port": 8787,
  "bind": "127.0.0.1",
  "dbPath": "gateway.db",
  "cookieSecure": false,
  "trustedOrigins": [],
  "rotation": "priority",
  "retry": {
    "maxKeysPerRequest": 3,
    "cooldownSeconds": 30,
    "respectRetryAfter": true,
    "maxRequestsPerKeyPerDay": null
  },
  "anthropic": {
    "aliases": {
      "claude-sonnet-5": "openai/gpt-4o-mini"
    }
  }
}
```

## Fields

| Field | Default | Meaning |
| --- | --- | --- |
| `port` | 8787 | HTTP listen port |
| `bind` | `127.0.0.1` | Listen address. Use `0.0.0.0` inside a container or VM |
| `dbPath` | `gateway.db` | SQLite database (created and migrated at boot, 0600) |
| `cookieSecure` | true | `Secure` flag on GUI session cookies. Set false for plain-HTTP local use |
| `trustedOrigins` | empty | Extra Origin hosts accepted on state-changing `/api` requests. Same-host is always allowed. Set your public host here when serving the GUI behind a reverse proxy |
| `rotation` | `priority` | `priority` (stick with failover) or `lru` (spread) |
| `apiKeys` | — | Deprecated: legacy first-boot migration input, ignored afterwards |
| `anthropic.aliases` | empty | Map of `claude-*` model names to `provider/model` values, applied on `/v1/messages` |
| `zen.baseUrl` | `https://opencode.ai/zen/v1` | zen adapter upstream |
| `zen.userAgent` | `opencode/1.18.32` | Client user agent; must satisfy the upstream's 1.18.0 floor (validated at startup) |
| `zen.injectTools` | true | Append the client-shape tool stubs to every zen request |
| `zen.responsesModels` | empty | Model IDs served on the Responses surface. The map also self-corrects: a signature 503 flips the surface for subsequent requests |
| `zen.freeOnly` | false | Restrict the zen catalog in `/v1/models` to models flagged as free upstream |
| `zen.modelMeta` | empty | Per-model metadata (context window, max output, modalities, reasoning) shown in `/v1/models`; unknown models get conservative defaults |
| `kilo.baseUrl` | `https://api.kilo.ai/api/gateway/v1` | kilo adapter upstream |
| `kilo.freeOnly` | false | Same catalog restriction as `zen.freeOnly` |
| `retry.maxKeysPerRequest` | 3 | Failover attempts per request |
| `retry.cooldownSeconds` | 30 | Default 429 cooldown |
| `retry.respectRetryAfter` | true | Prefer the upstream `Retry-After` duration |
| `retry.maxRequestsPerKeyPerDay` | null (off) | Optional per-key daily cap; an exhausted key cools until midnight |

## Environment

| Variable | Meaning |
| --- | --- |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | First superadmin's credentials. Required at first boot (bootstrap fails fast without them), create-if-absent on later boots; also used by `-reset-admin-password` |

For systemd, put them in an env file (for example
`/etc/nano-llm-proxy/gateway.env`, chmod 600) and load it with
`EnvironmentFile=`.

## CLI

```text
nano-llm-proxy [config.json [keys.json]] [-reset-admin-password]
```

- The config path defaults to `./config.json`; a missing file is fine, a
  malformed one is fatal.
- `keys.json` is a legacy first-boot seeding source
  (`{"zen":[{"label","key"}...],"kilo":[...]}`). Once imported into the
  database it is ignored — new deployments manage upstream keys in the GUI.
- `-reset-admin-password` resets the superadmin's password from
  `ADMIN_PASSWORD` and exits; restart the service afterwards.

## Deployment

### systemd

Build on any machine (Go 1.26+ and, for the GUI, Node 22+):

```bash
(cd web && npm ci && npm run build)
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .
scp nano-llm-proxy root@server:/usr/local/bin/
```

`/etc/nano-llm-proxy/gateway.env`:

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
EnvironmentFile=/etc/nano-llm-proxy/gateway.env
ExecStart=/usr/local/bin/nano-llm-proxy
Restart=on-failure
ProtectSystem=strict
ReadWritePaths=/var/lib/nano-llm-proxy

[Install]
WantedBy=multi-user.target
```

`ReadWritePaths` matters: without it, `ProtectSystem=strict` makes every
SQLite write fail. Logs go to journald: `journalctl -u nano-llm-proxy -f`.

### Docker

```bash
docker build -t nano-llm-proxy .
docker run -d --name nano-llm-proxy -p 8787:8787 \
  -v nano-llm-proxy-data:/data \
  -e ADMIN_EMAIL=admin@example.com -e ADMIN_PASSWORD=change-me \
  nano-llm-proxy
```

The image ships container-friendly defaults (`0.0.0.0` bind, plain-HTTP
cookies); the database lives in the `/data` volume. When fronting the
container with TLS, mount a config that sets `"cookieSecure": true`.

### Behind a reverse proxy

The GUI needs one of two things to accept state-changing requests:

- the browser's `Origin` host equals the backend `Host` (typical for Caddy
  and correctly configured nginx), or
- the host is listed in `trustedOrigins`.

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

Serve over TLS and set `cookieSecure: true` in production.

## Operations

- **Choose the primary key:** in `priority` rotation the first healthy key of
  a provider serves everything. Keys run in the order they were added to the
  provider; disable the busy one in the GUI to push traffic down the pool.
- **Re-enable / disable an upstream key:** GUI → Providers → key switch;
  effective immediately (pools rebuild in-request).
- **Cooldowns and pool counters are in-memory:** a restart clears them.
  Users, keys, and providers persist in SQLite; startup takes about two
  seconds.
- **Health:** `GET /health` is open and reports uptime and per-provider
  healthy-key counts.
