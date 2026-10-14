# Local mode

Local mode runs nano-llm-proxy on your own computer, for you alone: no
server, no sign-in, no admin rights. A one-line installer puts the binary in
your home folder, starts it in the background, and opens the GUI at
`http://localhost:8787`. Add your provider keys on the Providers page and
point your agents at it.

It is the same binary and the same gateway as a server deployment. Only the
mode differs.

## Local mode vs server mode

| | Server mode | Local mode |
| --- | --- | --- |
| Started with | `nano-llm-proxy` | `nano-llm-proxy local` (or `NANO_MODE=local`) |
| Who uses it | many users, with roles | one owner |
| GUI sign-in | email + password, optional Google | none: the GUI opens straight in |
| Client key on `/v1/*` and `/mcp` | required (`fg-…`) | any placeholder works, unless **Require an API key** is on |
| Listen address | anything (`NANO_BIND`) | loopback only; any other `NANO_BIND` is fatal at boot |
| Data | `gateway.db` and `.env` in the working directory | a per-user data folder (see [Where files live](#where-files-live)) |
| First boot | needs `ADMIN_EMAIL` / `ADMIN_PASSWORD` | needs nothing |
| Runs as | a systemd system service (`deploy.sh`, root) | a per-user background service, started at sign-in |
| Web tools | off until `scripts/install-web-tools.sh` (root) | on, with a per-user SearXNG on `127.0.0.1:8899` (Linux/macOS) |
| Extra request guards | Origin check on GUI mutations | also a Host check on every route, and Origin + JSON `Content-Type` checks on `/v1/*` and `/mcp` |
| Users page | yes | hidden |

## Install

No admin rights, no `sudo`. The installer downloads the release for your
OS and CPU, checks its SHA-256 against the release's `checksums.txt`, and
refuses to install on a mismatch.

macOS, Linux, WSL (bash):

```bash
curl -fsSL https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.sh | bash
```

Windows PowerShell:

```powershell
irm https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1 | iex
```

Windows CMD:

```bat
curl -fsSL https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.cmd -o install.cmd && install.cmd && del install.cmd
```

Supported: Linux and macOS on amd64 or arm64 (an x86_64 shell under Rosetta
gets the native arm64 build), Windows 10 or newer on amd64. The bash
installer needs `curl`, `tar`, `awk` and `sha256sum` or `shasum`. Run it as
your normal user: run as root, it installs for root only.

### Options

bash: pass options after `bash -s --`, for example
`curl -fsSL …/install.sh | bash -s -- --port 9000 --no-search`.

| Option | Meaning |
| --- | --- |
| `--version vX.Y.Z` | install this release instead of the latest (also `NANO_INSTALL_VERSION`) |
| `--port N` | serve the GUI and API on port N, 1024–65535 (default 8787) |
| `--no-search` | skip the SearXNG web-search setup |
| `--no-autostart` | don't start nano-llm-proxy at sign-in (start it yourself) |
| `--no-open` | don't open the browser at the end |
| `--uninstall` | remove the background services and the binary |
| `--purge` | with `--uninstall`: also delete the data folder |

`SEARXNG_REF` overrides the pinned SearXNG commit.

PowerShell: `irm | iex` can't take parameters, so set environment variables
first, or run the script as a scriptblock:

```powershell
$env:NANO_INSTALL_PORT = 9000; irm https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1 | iex
& ([scriptblock]::Create((irm https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1))) -Port 9000
```

| Parameter (file, `install.cmd`, scriptblock) | Environment (`irm \| iex`) |
| --- | --- |
| `-Version vX.Y.Z` | `NANO_INSTALL_VERSION` |
| `-Port N` | `NANO_INSTALL_PORT` |
| `-NoAutostart` | `NANO_INSTALL_NO_AUTOSTART=1` |
| `-NoOpen` | `NANO_INSTALL_NO_OPEN=1` |
| `-Uninstall` | `NANO_INSTALL_UNINSTALL=1` |
| `-Purge` | `NANO_INSTALL_PURGE=1` |

`install.cmd` passes its arguments through to `install.ps1`, e.g.
`install.cmd -Port 9000 -NoOpen`. Windows has no SearXNG setup, so there is
no `-NoSearch`.

### What the installer does

1. Downloads `nano-llm-proxy_<version>_<os>_<arch>.tar.gz` (`.zip` on
   Windows) and verifies it against `checksums.txt`.
2. Installs the binary:
   - Linux/macOS: `~/.local/bin/nano-llm-proxy`, the previous one kept as
     `nano-llm-proxy.bak`. If the new binary doesn't run, the old one is
     put back. If `~/.local/bin` isn't on your `PATH`, it prints the line to
     add.
   - Windows: `%LOCALAPPDATA%\Programs\nano-llm-proxy\`, holding
     `nano-llm-proxy.exe` (console) and `nano-llm-proxyw.exe` (no console
     window, used for the background instance); previous files kept as
     `*.bak`. That folder is added to your user `PATH`.
3. Linux/macOS only, unless `--no-search`: sets up SearXNG in the data
   folder (see [Web search](#web-search)).
4. Runs `nano-llm-proxy local install`, which creates the database, registers
   the background services, starts them, waits for `/health`, opens the
   browser, and prints the URLs.

Nothing outside your home folder (`%LOCALAPPDATA%` and `HKCU` on Windows) is
touched.

## Where files live

The data folder is created `0700`:

| OS | Data folder |
| --- | --- |
| Linux | `$XDG_DATA_HOME/nano-llm-proxy`, else `~/.local/share/nano-llm-proxy` |
| macOS | `~/Library/Application Support/nano-llm-proxy` |
| Windows | `%LOCALAPPDATA%\nano-llm-proxy` |

`nano-llm-proxy local datadir` prints it. Inside:

```text
nano-llm-proxy/
├── gateway.db            # the database (0600): providers, upstream keys, settings, usage
├── gateway.db-wal, -shm  # SQLite WAL files
├── .env                  # optional bootstrap overrides, e.g. NANO_PORT (0600)
├── nano-llm-proxy.log    # the gateway's log (0600)
└── searxng/              # Linux/macOS, when web search is set up
    ├── src/              # SearXNG checkout (pinned commit)
    ├── venv/             # its Python environment
    ├── settings.yml      # its settings (kept on re-install)
    ├── searxng.log
    └── install.log
```

Only the data folder's `.env` is read in local mode, never one in the
working directory. Variables in the real environment still win over it.

## Start at sign-in

By default the installer registers nano-llm-proxy (and SearXNG, when set up)
to start when you sign in, and starts it now:

| OS | Mechanism | Entry |
| --- | --- | --- |
| Linux, systemd user instance reachable | systemd user unit, `Restart=always` | `~/.config/systemd/user/nano-llm-proxy.service`, `nano-llm-proxy-searxng.service` |
| Linux, no systemd user instance | XDG autostart entry, plus an immediate background start | `~/.config/autostart/nano-llm-proxy.desktop`, `nano-llm-proxy-searxng.desktop` |
| macOS | launchd LaunchAgent, `RunAtLoad` + `KeepAlive` | `~/Library/LaunchAgents/dev.nano-llm-proxy.plist`, `dev.nano-llm-proxy.searxng.plist` |
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, value `NanoLLMProxy` | runs `nano-llm-proxyw.exe local` (no console window) |

Notes:

- The systemd user unit runs with your user session. To keep it running
  while you're signed out, enable lingering: `loginctl enable-linger`.
- systemd and launchd restart the gateway if it exits. The XDG entry and
  the Windows Run value only start it at sign-in; nothing restarts it.
- The XDG entry is run by a desktop session. On a machine without one (WSL
  without systemd, a bare server) nothing runs it: start nano-llm-proxy
  yourself with `nano-llm-proxy local`.
- Install with `--no-autostart` (`-NoAutostart`) to register nothing.

To change it later: **Settings → This computer → Start automatically when I
sign in**. The switch registers or removes the sign-in entry only. It takes
effect at your next sign-in and doesn't stop or start the running instance.

## Pointing agents at it

| Client | Setting |
| --- | --- |
| OpenAI-compatible | base URL `http://localhost:8787/v1` |
| Anthropic-compatible | base URL `http://localhost:8787` |
| MCP web tools | `http://localhost:8787/mcp` (streamable HTTP) |
| API key | any placeholder, e.g. `local` |

Any key works, or none, until you turn on **Settings → This computer →
Require an API key**. With it on, agents need a real client key from
Profile → My API Keys, exactly as in server mode.

Requests without a valid key are counted against the built-in key
`local (no key)` in Profile → My API Keys, so usage still shows up on the
Usage page; a real key is tracked under its own name. Disabling
`local (no key)` makes keyless requests fail with a 401 until you enable it
again. Deleting it is undone at the next start (it is recreated).

Claude Code, in `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://localhost:8787",
    "ANTHROPIC_AUTH_TOKEN": "local",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "provider/model-opus",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "provider/model-sonnet",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "provider/model-haiku"
  }
}
```

MCP web tools for Claude Code:

```bash
claude mcp add -s user -t http nano-web http://localhost:8787/mcp
```

Add `--header "Authorization: Bearer fg-…"` when **Require an API key** is
on. The in-app **Guide** page renders the same snippets for Claude Code, Pi
and other clients with your actual URL.

Use your port if you changed it.

## Web search

On Linux and macOS the installer sets up a private SearXNG for `web_search`:
a checkout of a pinned commit, a Python venv, and a `settings.yml` (JSON API
on, rate limiter off, a short list of engines that need no API key), all in
`<data folder>/searxng`. It listens on `127.0.0.1:8899` and runs as a second
background service. A fresh local database has web tools on and pointed at
that URL.

It needs Python 3.10 or newer (with `venv`) and `git`. Without them the
installer skips web search with a warning and everything else still works;
install them and re-run the installer to add it (macOS:
`brew install python git`; Debian/Ubuntu: `sudo apt install python3-venv`).
The first setup takes a few minutes. `settings.yml` is kept on re-install:
delete it to regenerate.

On Windows there is no SearXNG setup. Set the URL of a SearXNG you can reach
in **Settings → Web tools** (its JSON API must be on). `web_read` works
without it.

`web_read` fetches pages natively and escalates to the
[obscura](https://github.com/h4ckf0r0day/obscura) headless browser for
JavaScript-heavy pages. The installer doesn't install obscura; if you
install it, set its absolute path in **Settings → Web tools** (the
background service may not have your shell's `PATH`). `web_read` refuses
loopback and private addresses in local mode too, so it can't read pages
served from your own machine.

## Changing the port

- **Settings → This computer → Change port** (1024–65535). The port is
  saved to `<data folder>/.env`. Under a systemd user unit or a LaunchAgent,
  nano-llm-proxy restarts itself on the new port and the GUI follows it.
  On Windows and with the XDG fallback there is no supervisor: stop it and
  start it again (`nano-llm-proxy local`, or sign out and back in).
- Or re-run the installer with `--port N` (`-Port N`).
- Or set `NANO_PORT=N` in `<data folder>/.env` and restart.

A `NANO_PORT` in the real environment overrides the `.env` value. Update
your agents' base URLs after a change.

## The `local` command

```text
nano-llm-proxy local                      run in local mode (foreground)
nano-llm-proxy local install [--port N] [--no-autostart] [--no-open] [--quiet]
nano-llm-proxy local uninstall [--purge]
nano-llm-proxy local status
nano-llm-proxy local datadir
```

- `local` runs the gateway in the foreground, logging to the terminal and the
  log file. `NANO_MODE=local nano-llm-proxy` is the same. `NANO_MODE` is
  read from the real environment only, not from a `.env` file. Don't run it
  while the background instance is up: both would want the same port.
- `local install` is what the installers run: it creates the database,
  registers and starts the background services (restarting running ones, so
  an upgrade runs the new binary), and opens the browser. `--port` writes
  `NANO_PORT` to the data folder's `.env`; `--quiet` skips its closing
  summary (the installers print their own).
- `local uninstall` removes the background services and their sign-in
  entries. `--purge` also deletes the data folder: the database with your
  provider keys, the log, SearXNG. The binary stays; the installers'
  `--uninstall` removes it too.
- `local status` prints the data folder, the address, whether it answers on
  `/health`, and each service's sign-in registration and running state.
- `local datadir` prints the data folder (creating it if needed).

## Security model

Local mode has no sign-in, so it relies on being reachable only from your
computer:

- **Loopback only.** It listens on `127.0.0.1` by default. `NANO_BIND` may
  be `127.0.0.1`, `localhost` or `::1`; anything else is fatal at boot.
- **Host check.** Every request, GUI included, must carry a `Host` of
  `localhost`, `127.0.0.1` or `::1` with the gateway's port; anything else
  gets a 403. This stops DNS rebinding: a web page whose own hostname
  resolves to `127.0.0.1` still can't drive the gateway.
- **Web pages can't call the API.** On `/v1/*` and `/mcp`, a request with an
  `Origin` header that isn't a loopback host gets a 403, and a `POST`
  without a JSON `Content-Type` gets a 415. A cross-site page can't send a
  JSON request without a CORS preflight, which the gateway never approves.
  GUI mutations keep the usual same-origin check; `NANO_TRUSTED_ORIGINS` is
  ignored in local mode.
- **Anyone with a shell on this computer has full access.** The GUI and
  `/api/*` need no credentials, so any program or user that can reach
  `localhost` can manage providers and spend your credits. **Require an API
  key** limits only `/v1/*` and `/mcp`. Don't use local mode on a shared
  machine; use server mode with accounts there.
- **Files.** The data folder is created `0700`; `gateway.db`, `.env` and the
  log are `0600`.
- **Provider keys are stored reversibly** in `gateway.db`, as in server mode:
  they must be sent upstream verbatim. Anyone who can read the file can read
  them. Use full-disk encryption if that matters to you.
- The GUI never shows upstream keys, and the log never contains them.

## Upgrading

Re-run the installer. It downloads the latest release (or `--version`),
keeps the previous binary as `.bak`, and restarts the running background
instance on the new binary. Your data folder, settings and SearXNG are kept.
On Windows it stops the running copies first, since Windows locks a running
`.exe`.

## Uninstalling

```bash
curl -fsSL https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.sh | bash -s -- --uninstall
```

```powershell
$env:NANO_INSTALL_UNINSTALL = 1; irm https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1 | iex
```

This removes the services, the sign-in entries and the binary (on Windows
also the program folder and its `PATH` entry). The data folder is kept;
add `--purge` (`NANO_INSTALL_PURGE=1`) to delete it as well.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Anything | `nano-llm-proxy local status`, then the log: `<data folder>/nano-llm-proxy.log` (also shown under Settings → This computer). It is truncated at startup once it passes 5 MiB |
| The installer warns that nano-llm-proxy did not answer | the log; often the port is taken. Re-run with `--port N` |
| `403 … this address is not allowed` | open `http://localhost:<port>` exactly; other hostnames and addresses are refused (Host check) |
| `403 Requests from web pages are not allowed` | the request came from a browser page on another site; call the API from your agent, not a web page |
| `415 Content-Type must be application/json` | send `POST` bodies with `Content-Type: application/json` |
| `401 Invalid or missing API key` | **Require an API key** is on: use a key from Profile → My API Keys |
| `401 The built-in local key is disabled` | enable `local (no key)` in Profile → My API Keys, or send a real key |
| `web_search` fails | Settings → This computer shows whether SearXNG is installed and running; its log is `searxng/searxng.log` and the setup log `searxng/install.log` in the data folder |
| `nano-llm-proxy: command not found` | add `~/.local/bin` to `PATH` (the installer printed the line), or open a new terminal on Windows |
| Service details (Linux) | `systemctl --user status nano-llm-proxy`, `journalctl --user -u nano-llm-proxy` |
| Service details (macOS) | `launchctl print gui/$(id -u)/dev.nano-llm-proxy` |
