# Deployment

nano-llm-proxy ships as one static Go binary with an embedded admin GUI and a
single SQLite file — no Docker, sized for small (512 MB) VPS or container
hosts. The gateway itself needs no external services; the optional MCP web
tools add two self-hosted companions (SearXNG + obscura) with their own
installer, documented under [Companion
services](#companion-services--web-search--reader-optional) below. This note
covers deploying to a Linux machine; bootstrap environment variables are
documented in [configuration.md](configuration.md).

## Prerequisites

| Requirement | Needed for |
| --- | --- |
| Go (see `go.mod`) | building the binary |
| Node.js + npm | building the admin GUI (skip only if you ship a GUI-less binary) |
| systemd | the service install done by `deploy.sh` |
| a `.env` file | both deploy paths — copy `.env.example` and fill it in |

`ADMIN_EMAIL` and `ADMIN_PASSWORD` must be set at first boot: the bootstrap
refuses to start without them and creates the first superadmin account. Later
boots use the database and ignore them (unless you run
`nano-llm-proxy -reset-admin-password`).

## Option A — the deployment script (recommended)

From a checkout of this repository on the target machine:

```bash
cp .env.example .env      # then edit: at minimum ADMIN_EMAIL/ADMIN_PASSWORD
sudo ./deploy.sh
```

`deploy.sh` performs the whole setup:

1. Builds the GUI (`web/`) — it is embedded into the binary only under the
   `prod` build tag, so this step is what puts the admin UI inside the binary.
2. Builds a static binary (`CGO_ENABLED=0 go build -tags prod`).
3. Installs binary + `.env` to `/opt/nano-llm-proxy`, owned by an
   unprivileged service user (`nano`) that it creates if missing.
4. Writes and enables a hardened systemd unit (`ProtectSystem=strict` with
   `ReadWritePaths` limited to the app dir) and starts the service.

Two knobs, both optional: `NANO_DEPLOY_DIR` overrides the install directory
and `NANO_SERVICE_USER` overrides the service user.

## Managing the service

```bash
systemctl status nano-llm-proxy
journalctl -u nano-llm-proxy -f
curl -s http://127.0.0.1:8787/health
```

`/health` returns JSON with per-provider key-pool health; it is the quickest
post-deploy check. The GUI lives on the same port.

## Updating a running deployment

Re-running `sudo ./deploy.sh` is always safe: it rebuilds, replaces the
binary, and restarts the service. The database and `.env` are untouched.

To update a remote host from a build machine (backup-first swap):

```bash
# on the build machine — GUI must be built before the binary embeds it
(cd web && npm ci && npm run build)
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .

# on the target host: swap with a backup, then restart
scp nano-llm-proxy user@host:/opt/nano-llm-proxy/nano-llm-proxy.new
ssh user@host 'cp /opt/nano-llm-proxy/nano-llm-proxy /opt/nano-llm-proxy/nano-llm-proxy.bak-pre-update \
  && mv /opt/nano-llm-proxy/nano-llm-proxy.new /opt/nano-llm-proxy/nano-llm-proxy \
  && systemctl restart nano-llm-proxy'
curl -s http://host:8787/health
```

If the new build misbehaves, restoring the `.bak-pre-update` copy and
restarting rolls back in seconds.

## Option B — manual, without the script

The script has no magic; the equivalent by hand is:

```bash
(cd web && npm ci && npm run build)   # GUI → embedded with the prod tag
CGO_ENABLED=0 go build -tags prod -o nano-llm-proxy .
install -m 0755 nano-llm-proxy /usr/local/bin/
```

Then run it under any process supervisor with the working directory (or
`NANO_DB_PATH`) pointing at a writable location — the SQLite file is created
there on first boot. A minimal hardened unit is shown in the README's
Deploying section; keep `ReadWritePaths` covering the database directory or
`ProtectSystem=strict` will make every write fail.

## Companion services — web search & reader (optional)

The gateway's MCP endpoint (`POST /mcp`) serves `web_search` and `web_read`
to every client key. Search runs on a **self-hosted SearXNG**; page reads
use a native Go fetch and escalate to the **obscura** headless browser for
JavaScript-heavy or bot-protected pages. Both are external to the binary and
installed by one script:

```bash
sudo ./scripts/install-web-tools.sh              # install/update both
sudo ./scripts/install-web-tools.sh --check      # verify (no root, no writes)
sudo ./scripts/install-web-tools.sh --update     # bump to the pinned refs + restart
sudo ./scripts/install-web-tools.sh --skip-searxng           # obscura only
sudo ./scripts/install-web-tools.sh --obscura-dir ~/obscura  # offline: prebuilt binaries
```

The script is idempotent — re-running always converges. For SearXNG it
installs system packages, creates a `searxng` system user, clones the repo
at a pinned commit (`SEARXNG_REF` env overrides), builds a venv, writes
`/etc/searxng/settings.yml` (only if absent — your secret and edits are
preserved) with the JSON API enabled and a trimmed no-API-key engine list,
and installs a hardened systemd unit running the built-in server on
`127.0.0.1:8888` with `MemoryMax=280M`. For obscura it downloads the pinned
release (`OBSCURA_VERSION` env overrides) into `/usr/local/bin`, replacing
binaries atomically.

After installing, open the admin GUI → **Settings → Web tools**, enable it,
and use the **Test SearXNG / Test obscura** buttons; `--check` verifies all
three pieces (SearXNG JSON, obscura, gateway flag) any time.

Memory budget on a 512 MB host: gateway ~30–60 MB + SearXNG ~150–200 MB
(capped at 280 MB) + each obscura render 33–105 MB transient. Two
concurrent renders fit; if the box starts swapping, drop
`webTools.obscuraConcurrency` to 1 in Settings. SearXNG engines can get
CAPTCHA-blocked on residential IPs — if searches come back consistently
empty, trim the engine list further in `/etc/searxng/settings.yml`.

Updating the pins: `SEARXNG_REF` tracks a master commit (SearXNG publishes
no tags) and `OBSCURA_VERSION` a release tag; check upstream and pass the
new value (or export the env var) with `--update`. The `deploy.sh` app flow
never touches these services — updating the gateway binary is independent
of them.

## Binding and TLS notes

- `NANO_BIND` defaults to `127.0.0.1`. Set `0.0.0.0` (or a specific address)
  when clients reach the host directly.
- Keep `NANO_COOKIE_SECURE=true` (the default) whenever the GUI is reachable
  over TLS; set it false only for plain-HTTP local use.
- Behind a reverse proxy serving the GUI under a different host name, add that
  host to `NANO_TRUSTED_ORIGINS` so state-changing `/api` requests pass the
  Origin check. Terminate TLS at the proxy; the proxy is also a natural place
  to put the gateway behind an existing domain.
