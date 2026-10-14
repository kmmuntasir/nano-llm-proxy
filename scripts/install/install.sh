#!/usr/bin/env bash
# nano-llm-proxy installer for Linux and macOS (local mode, per user, no sudo).
#
#   curl -fsSL https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.sh | bash
#   curl -fsSL .../install.sh | bash -s -- --port 9000 --no-search
#   curl -fsSL .../install.sh | bash -s -- --uninstall [--purge]
#
# What it does:
#   1. downloads the release archive for this OS/CPU and checks its SHA-256
#      against the release's checksums.txt;
#   2. installs the binary to ~/.local/bin/nano-llm-proxy (the previous one is
#      kept as nano-llm-proxy.bak);
#   3. optionally sets up a private SearXNG (web search) in the data folder;
#   4. uses your obscura (the headless browser web_read uses for JavaScript-
#      heavy pages) if one is on PATH, else downloads the pinned release into
#      the data folder and checks its SHA-256;
#   5. runs `nano-llm-proxy local install`, which registers the background
#      services, starts them and opens the browser.
#
# Re-running upgrades in place. Nothing outside your home folder is touched.
#
# Test overrides: NANO_INSTALL_BASE_URL (a directory URL holding the archives
# and checksums.txt; skips GitHub), NANO_INSTALL_VERSION (same as --version),
# NANO_INSTALL_OBSCURA_BASE_URL (a directory URL holding the obscura archives;
# they must still match the pinned hashes below).

set -euo pipefail

REPO="kmmuntasir/nano-llm-proxy"
DEFAULT_PORT=8787
BIN_DIR="$HOME/.local/bin"
BIN="$BIN_DIR/nano-llm-proxy"

# Same pin as scripts/install-web-tools.sh (SearXNG publishes no tags).
SEARXNG_REF="${SEARXNG_REF:-d8ae3abd59b6cb3970e3380e484c7d4cd8529619}"
SEARXNG_REPO="https://github.com/searxng/searxng.git"
SEARXNG_PORT=8899

# v0.2.4 rather than the server script's v0.2.3: both publish the Linux,
# macOS and Windows builds local mode needs, and v0.2.4 adds SSRF-gate and
# frame-teardown crash fixes. obscura publishes no checksums file, so the
# SHA-256 of each stealth archive is pinned here (taken from the GitHub
# release's asset digests); a bump must update all of them, and install.ps1.
OBSCURA_VERSION="v0.2.4"
OBSCURA_REPO="https://github.com/h4ckf0r0day/obscura"
obscura_sha256() { # asset — the pinned hash, empty for an unknown asset
    case "$1" in
        obscura-x86_64-linux-stealth.tar.gz) echo 49b53f74a509764c42a35c8e73a37301399e1500d43564fad4cb641721efc753 ;;
        obscura-aarch64-linux-stealth.tar.gz) echo 56eacea66e4a5b0ab0f39343183c308816b426f604259b9cc41402488bf998f7 ;;
        obscura-x86_64-macos-stealth.tar.gz) echo 0fff3953a0484f505bc4445e8fab3d718084c7d0bd704dc05d2958e49b1ad4c7 ;;
        obscura-aarch64-macos-stealth.tar.gz) echo 183ce21fc9caf421c4e027cd8043970224a24d9921d8bf27105153ca74f0e3ae ;;
    esac
}

# --- output -----------------------------------------------------------------

if [[ -t 2 ]]; then
    C_OK=$'\033[1;32m' C_WARN=$'\033[1;33m' C_ERR=$'\033[1;31m' C_OFF=$'\033[0m'
else
    C_OK="" C_WARN="" C_ERR="" C_OFF=""
fi
say() { printf '%s==>%s %s\n' "$C_OK" "$C_OFF" "$*"; }
warn() { printf '%swarning:%s %s\n' "$C_WARN" "$C_OFF" "$*" >&2; }
die() { printf '%serror:%s %s\n' "$C_ERR" "$C_OFF" "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
nano-llm-proxy installer (Linux, macOS)

Usage: install.sh [options]
       curl -fsSL <url>/install.sh | bash -s -- [options]

Options:
  --version vX.Y.Z   install this release instead of the latest
  --port N           serve the GUI and API on port N (default 8787)
  --no-search        skip the local SearXNG web-search setup
  --no-obscura       skip obscura (the headless browser web_read uses for
                     JavaScript-heavy pages)
  --no-autostart     do not start nano-llm-proxy when you sign in
  --no-open          do not open the browser at the end
  --uninstall        stop and remove the services and the binary
  --purge            with --uninstall: also delete the data folder
  -h, --help         show this help
EOF
}

# --- arguments --------------------------------------------------------------

VERSION="${NANO_INSTALL_VERSION:-}"
PORT=""
NO_SEARCH=0
NO_OBSCURA=0
NO_AUTOSTART=0
NO_OPEN=0
UNINSTALL=0
PURGE=0

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --version) [[ $# -ge 2 ]] || die "--version needs a value (e.g. v0.3.0)"; VERSION="$2"; shift ;;
            --version=*) VERSION="${1#*=}" ;;
            --port) [[ $# -ge 2 ]] || die "--port needs a value"; PORT="$2"; shift ;;
            --port=*) PORT="${1#*=}" ;;
            --no-search) NO_SEARCH=1 ;;
            --no-obscura) NO_OBSCURA=1 ;;
            --no-autostart) NO_AUTOSTART=1 ;;
            --no-open) NO_OPEN=1 ;;
            --uninstall) UNINSTALL=1 ;;
            --purge) PURGE=1 ;;
            -h|--help) usage; exit 0 ;;
            *) die "unknown option: $1 (see --help)" ;;
        esac
        shift
    done
    if [[ -n $PORT ]]; then
        if ! [[ $PORT =~ ^[0-9]+$ ]] || ((PORT < 1024 || PORT > 65535)); then
            die "--port must be a number from 1024 to 65535"
        fi
    fi
    if [[ $PURGE -eq 1 && $UNINSTALL -eq 0 ]]; then
        die "--purge only works together with --uninstall"
    fi
    return 0
}

# --- platform ---------------------------------------------------------------

detect_platform() {
    case "$(uname -s)" in
        Linux) OS=linux ;;
        Darwin) OS=darwin ;;
        MINGW* | MSYS* | CYGWIN* | Windows_NT)
            die "this installer is for Linux and macOS. On Windows run in PowerShell:
  irm https://github.com/$REPO/releases/latest/download/install.ps1 | iex" ;;
        *) die "unsupported operating system: $(uname -s) (Linux and macOS only)" ;;
    esac
    case "$(uname -m)" in
        x86_64 | amd64) ARCH=amd64 ;;
        aarch64 | arm64) ARCH=arm64 ;;
        *) die "unsupported CPU: $(uname -m) (amd64 and arm64 only)" ;;
    esac
    # An x86_64 shell under Rosetta on Apple silicon: install the native build.
    if [[ $OS == darwin && $ARCH == amd64 ]] \
        && [[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || true)" == 1 ]]; then
        ARCH=arm64
    fi
}

need() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# --- download + verify ------------------------------------------------------

# Sets BASE (download directory URL), VER (no leading v) and ASSET.
resolve_release() {
    VER="${VERSION#v}"
    if [[ -n ${NANO_INSTALL_BASE_URL:-} ]]; then
        BASE="${NANO_INSTALL_BASE_URL%/}"
    else
        if [[ -z $VER ]]; then
            say "Looking up the latest release"
            # releases/latest redirects to releases/tag/<tag>; no API token needed.
            local url
            url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
                "https://github.com/$REPO/releases/latest") \
                || die "could not reach github.com to find the latest release"
            VER="${url##*/}"
            VER="${VER#v}"
            [[ $VER =~ ^[0-9]+\.[0-9]+\.[0-9]+ ]] \
                || die "could not work out the latest release (got $url)"
        fi
        BASE="https://github.com/$REPO/releases/download/v$VER"
    fi
    ASSET=""
    [[ -z $VER ]] || ASSET="nano-llm-proxy_${VER}_${OS}_${ARCH}.tar.gz"
}

download() { # url dest
    curl -fsSL --retry 3 -o "$2" "$1" || die "download failed: $1"
}

download_and_verify() {
    download "$BASE/checksums.txt" "$TMP/checksums.txt"
    if [[ -z $ASSET ]]; then
        # NANO_INSTALL_BASE_URL without a version: take the one checksums.txt lists.
        ASSET=$(awk -v os="$OS" -v arch="$ARCH" \
            '$2 ~ ("^nano-llm-proxy_.*_" os "_" arch "\\.tar\\.gz$") { print $2; exit }' \
            "$TMP/checksums.txt")
        [[ -n $ASSET ]] || die "no $OS/$ARCH archive listed in $BASE/checksums.txt"
        VER="${ASSET#nano-llm-proxy_}"
        VER="${VER%_"${OS}"_"${ARCH}".tar.gz}"
    fi

    local expected actual
    expected=$(awk -v f="$ASSET" '$2 == f { print $1; exit }' "$TMP/checksums.txt")
    [[ -n $expected ]] || die "$ASSET is not listed in checksums.txt (no build for $OS/$ARCH in v$VER?)"

    say "Downloading nano-llm-proxy v$VER ($OS/$ARCH)"
    download "$BASE/$ASSET" "$TMP/$ASSET"
    actual=$(sha256_of "$TMP/$ASSET")
    [[ $actual == "$expected" ]] || die "checksum mismatch for $ASSET — refusing to install
  expected $expected
  got      $actual"
    say "Checksum verified"

    mkdir -p "$TMP/extract"
    tar -xzf "$TMP/$ASSET" -C "$TMP/extract"
    [[ -f "$TMP/extract/nano-llm-proxy" ]] || die "$ASSET does not contain nano-llm-proxy"
}

# --- binary -----------------------------------------------------------------

install_binary() {
    mkdir -p "$BIN_DIR"
    cp "$TMP/extract/nano-llm-proxy" "$BIN.new"
    chmod 0755 "$BIN.new"
    if [[ -f $BIN ]]; then
        cp -p "$BIN" "$BIN.bak"
    fi
    # mv is atomic: a running instance keeps its old file until it restarts.
    mv -f "$BIN.new" "$BIN"

    if ! "$BIN" -version >/dev/null 2>&1; then
        if [[ -f "$BIN.bak" ]]; then
            mv -f "$BIN.bak" "$BIN"
            die "the downloaded binary does not run on this machine; the previous one was restored"
        fi
        die "the downloaded binary does not run on this machine"
    fi
    say "Installed $BIN"
}

path_hint() {
    case ":$PATH:" in
        *":$BIN_DIR:"*) return 0 ;;
    esac
    local rc
    # shellcheck disable=SC2088 # "~/..." is display text, not a path we open
    case "${SHELL##*/}" in
        zsh) rc="~/.zshrc" ;;
        bash) if [[ $OS == darwin ]]; then rc="~/.bash_profile"; else rc="~/.bashrc"; fi ;;
        fish) rc="" ;;
        *) rc="~/.profile" ;;
    esac
    echo
    warn "$BIN_DIR is not on your PATH. To run nano-llm-proxy by name, add this line"
    if [[ -n $rc ]]; then
        # shellcheck disable=SC2016 # printed literally for the user's rc file
        printf '  to %s and open a new terminal:\n\n    export PATH="$HOME/.local/bin:$PATH"\n\n' "$rc" >&2
    else
        printf '  to fish (run once):\n\n    fish_add_path ~/.local/bin\n\n' >&2
    fi
}

# --- SearXNG (optional web search) -----------------------------------------

SEARCH_STATUS=""

skip_search() { # reason [hint]
    warn "web search skipped: $1"
    [[ -z ${2:-} ]] || warn "$2"
    warn "everything else works; re-run the installer later to add web search"
    SEARCH_STATUS="off ($1)"
}

find_python() { # prints the first python3 >= 3.10
    local c
    for c in python3 python3.14 python3.13 python3.12 python3.11 python3.10; do
        if command -v "$c" >/dev/null 2>&1 \
            && "$c" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 10) else 1)' 2>/dev/null; then
            command -v "$c"
            return 0
        fi
    done
    return 1
}

searxng_at_ref() { # src — true when the checkout is exactly $SEARXNG_REF
    [[ -d "$1/.git" ]] && [[ "$(git -C "$1" rev-parse HEAD 2>/dev/null)" == "$SEARXNG_REF" ]]
}

# Called inside `if`, where set -e is off: every step returns explicitly.
fetch_searxng() { # src
    if [[ ! -d "$1/.git" ]]; then
        rm -rf "$1"
        git init --quiet "$1" || return 1
        git -C "$1" remote add origin "$SEARXNG_REPO" || return 1
    fi
    git -C "$1" fetch --quiet --depth 1 origin "$SEARXNG_REF" || return 1
    git -C "$1" checkout --quiet --force FETCH_HEAD || return 1
    # a checkout that silently no-ops would fail pip with "not a Python project"
    [[ -f "$1/setup.py" || -f "$1/pyproject.toml" ]] || return 1
}

# Same steps as scripts/install-web-tools.sh. --no-build-isolation needs the
# build deps in the venv itself: SearXNG's setup.py imports msgspec, and some
# venvs ship without setuptools.
pip_install_searxng() { # venv src
    "$1/bin/pip" install --quiet --disable-pip-version-check \
        setuptools wheel pyyaml msgspec typing-extensions pybind11 || return 1
    "$1/bin/pip" install --quiet --disable-pip-version-check --use-pep517 \
        --no-build-isolation -e "$2" || return 1
}

random_hex() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 32
    else
        od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    fi
}

# Trimmed from scripts/templates/searxng-settings.yml: loopback only, JSON API
# on (the gateway's web_search uses format=json), limiter off (no Redis), and
# only engines that need no API key.
write_searxng_settings() { # file
    local secret
    secret=$(random_hex)
    (
        umask 077
        cat >"$1.new" <<EOF
# SearXNG settings for nano-llm-proxy local mode (written by install.sh).
# Kept on re-install; delete this file to regenerate it.
use_default_settings:
  # keep_only is what actually trims: plain "true" would keep every default
  # engine (80+), with the list below only re-enabling these
  engines:
    keep_only:
      - duckduckgo
      - duckduckgo web
      - brave
      - bing
      - qwant
      - wikipedia
      - stackoverflow
      - github

server:
  secret_key: "$secret"
  bind_address: "127.0.0.1"
  port: $SEARXNG_PORT
  limiter: false
  image_proxy: false

search:
  safe_search: 1
  formats:
    - html
    - json

engines:
  - name: duckduckgo
    disabled: false
  - name: duckduckgo web
    disabled: false
  - name: brave
    disabled: false
  - name: bing
    disabled: false
  - name: qwant
    disabled: false
  - name: wikipedia
    disabled: false
  - name: stackoverflow
    disabled: false
  - name: github
    disabled: false
EOF
    )
    mv -f "$1.new" "$1"
}

setup_search() {
    if [[ $NO_SEARCH -eq 1 ]]; then
        SEARCH_STATUS="off (--no-search)"
        return 0
    fi
    local hint="" py
    [[ $OS == darwin ]] && hint="on macOS: brew install python git"
    if ! py=$(find_python); then
        skip_search "it needs Python 3.10 or newer" "$hint"
        return 0
    fi
    if ! command -v git >/dev/null 2>&1; then
        skip_search "it needs git" "$hint"
        return 0
    fi

    local sx="$DATADIR/searxng"
    local src="$sx/src" venv="$sx/venv" log="$sx/install.log" fetched=0
    mkdir -p "$sx"
    : >"$log"

    say "Setting up web search (SearXNG ${SEARXNG_REF:0:12})"
    if ! searxng_at_ref "$src"; then
        say "  fetching SearXNG"
        if ! fetch_searxng "$src" >>"$log" 2>&1; then
            skip_search "could not download SearXNG (details: $log)"
            return 0
        fi
        fetched=1
    fi

    if [[ $fetched -eq 1 ]] || ! "$venv/bin/python" -c 'import searx' >/dev/null 2>&1; then
        say "  installing Python packages (a few minutes the first time)"
        rm -rf "$venv"
        if ! "$py" -m venv "$venv" >>"$log" 2>&1; then
            rm -rf "$venv"
            skip_search "python3 -m venv failed (details: $log)" \
                "on Debian/Ubuntu: sudo apt install python3-venv"
            return 0
        fi
        if ! pip_install_searxng "$venv" "$src" >>"$log" 2>&1; then
            # no venv = `local install` won't register a SearXNG service that can't start
            rm -rf "$venv"
            skip_search "pip install failed (details: $log)"
            return 0
        fi
    fi

    if [[ ! -f "$sx/settings.yml" ]]; then
        write_searxng_settings "$sx/settings.yml"
    fi
    say "  SearXNG ready (it will listen on 127.0.0.1:$SEARXNG_PORT)"
    SEARCH_STATUS="on (local SearXNG, 127.0.0.1:$SEARXNG_PORT)"
}

# --- obscura (optional page rendering for web_read) ---------------------------

OBSCURA_PATH=""   # absolute path handed to `local install --obscura`
OBSCURA_STATUS=""

obscura_version_of() { # bin — prints e.g. 0.2.4 ("obscura 0.2.4"), empty if it won't run
    "$1" --version 2>/dev/null | awk 'NR == 1 { print $2 }' || true
}

# The user's own obscura on PATH, as an absolute path. One inside our data
# folder doesn't count: that copy is ours to upgrade.
find_user_obscura() {
    local p dir
    p=$(command -v obscura 2>/dev/null) || return 1
    [[ $p == /* ]] || p="$(cd "$(dirname "$p")" && pwd)/$(basename "$p")"
    dir=$(dirname "$p")
    [[ $dir != "$DATADIR/bin" ]] || return 1
    printf '%s\n' "$p"
}

# Called inside `if`, where set -e is off: every step returns explicitly.
download_obscura() { # bindir
    local arch os asset expected actual url x="$TMP/obscura"
    case "$ARCH" in amd64) arch=x86_64 ;; arm64) arch=aarch64 ;; esac
    case "$OS" in linux) os=linux ;; darwin) os=macos ;; esac
    asset="obscura-$arch-$os-stealth.tar.gz"
    expected=$(obscura_sha256 "$asset")
    [[ -n $expected ]] || { warn "no pinned obscura build for $os/$arch"; return 1; }
    url="${NANO_INSTALL_OBSCURA_BASE_URL:-$OBSCURA_REPO/releases/download/$OBSCURA_VERSION}"
    url="${url%/}/$asset"

    say "  downloading obscura $OBSCURA_VERSION ($os/$arch, about 75 MB)"
    rm -rf "$x" && mkdir -p "$x" || return 1
    curl -fsSL --retry 3 -o "$x/$asset" "$url" || { warn "download failed: $url"; return 1; }
    actual=$(sha256_of "$x/$asset")
    if [[ $actual != "$expected" ]]; then
        warn "checksum mismatch for $asset; not installing it (expected $expected, got $actual)"
        return 1
    fi
    tar -xzf "$x/$asset" -C "$x" || { warn "could not unpack $asset"; return 1; }
    [[ -f "$x/obscura" ]] || { warn "$asset does not contain obscura"; return 1; }

    mkdir -p "$1" || return 1
    # .new + mv: a fetch running the old copy keeps its file until it exits
    cp "$x/obscura" "$1/obscura.new" && chmod 755 "$1/obscura.new" \
        && mv -f "$1/obscura.new" "$1/obscura" || return 1
    if [[ -f "$x/obscura-worker" ]]; then
        cp "$x/obscura-worker" "$1/obscura-worker.new" && chmod 755 "$1/obscura-worker.new" \
            && mv -f "$1/obscura-worker.new" "$1/obscura-worker" || return 1
    else
        rm -f "$1/obscura-worker" # never pair a new obscura with an old worker
    fi
    rm -rf "$x"
}

setup_obscura() {
    if [[ $NO_OBSCURA -eq 1 ]]; then
        OBSCURA_STATUS="not installed (--no-obscura)"
        return 0
    fi
    local p bindir="$DATADIR/bin" want="${OBSCURA_VERSION#v}" have
    if p=$(find_user_obscura); then
        say "Page rendering: using your obscura at $p"
        OBSCURA_PATH="$p"
        OBSCURA_STATUS="$p (yours)"
        return 0
    fi

    have=$(obscura_version_of "$bindir/obscura")
    if [[ $have == "$want" ]]; then
        say "Page rendering: obscura $want already installed"
    else
        say "Page rendering: installing obscura (the headless browser web_read uses for JavaScript-heavy pages)"
        if ! download_obscura "$bindir"; then
            if [[ -n $have ]]; then
                warn "keeping the obscura $have already in $bindir"
            else
                warn "obscura skipped: web_read still reads ordinary pages, only JavaScript-heavy ones fail"
                warn "re-run the installer later to add it"
                OBSCURA_STATUS="not installed (download failed)"
                return 0
            fi
        else
            have=$(obscura_version_of "$bindir/obscura")
            if [[ -z $have ]]; then
                rm -f "$bindir/obscura" "$bindir/obscura-worker"
                warn "the downloaded obscura does not run on this machine; removed it"
                warn "web_read still reads ordinary pages, only JavaScript-heavy ones fail"
                OBSCURA_STATUS="not installed (does not run here)"
                return 0
            fi
        fi
    fi
    OBSCURA_PATH="$bindir/obscura"
    OBSCURA_STATUS="$OBSCURA_PATH (obscura $have)"
}

# --obscura arrived with this installer; an older --version binary lacks it.
binary_takes_obscura() {
    local help
    help=$("$BIN" local help 2>/dev/null) || return 1
    [[ $help == *"--obscura"* ]]
}

# --- local install / summary -------------------------------------------------

current_port() { # the port from <datadir>/.env, else the default
    local p=""
    if [[ -f "$DATADIR/.env" ]]; then
        p=$(sed -nE "s/^[[:space:]]*(export[[:space:]]+)?NANO_PORT[[:space:]]*=[[:space:]]*[\"']?([0-9]+).*/\2/p" \
            "$DATADIR/.env" | tail -n 1)
    fi
    echo "${p:-$DEFAULT_PORT}"
}

run_local_install() {
    local args=(local install --quiet) # the summary below replaces its own
    [[ -z $PORT ]] || args+=(--port "$PORT")
    [[ $NO_AUTOSTART -eq 0 ]] || args+=(--no-autostart)
    [[ $NO_OPEN -eq 0 ]] || args+=(--no-open)
    if [[ -n $OBSCURA_PATH ]]; then
        if binary_takes_obscura; then
            args+=(--obscura "$OBSCURA_PATH")
        else
            warn "nano-llm-proxy v$VER can't be told where obscura is; set Settings > Web tools > Obscura path to $OBSCURA_PATH"
        fi
    fi
    say "Starting nano-llm-proxy"
    # tee: show its output as it comes, and keep it for the summary
    "$BIN" "${args[@]}" | tee "$TMP/local-install.out" \
        || die "'nano-llm-proxy ${args[*]}' failed (see the message above)"
    # a path the user set in Settings wins over ours (see `local install --obscura`)
    local kept
    kept=$(sed -n 's/^obscura: keeping your own path \(.*\) (Settings > Web tools).*$/\1/p' \
        "$TMP/local-install.out")
    [[ -z $kept ]] || OBSCURA_STATUS="$kept (yours, set in Settings > Web tools)"
    if grep -q '^warning: obscura path .* not saved' "$TMP/local-install.out"; then
        OBSCURA_STATUS="$OBSCURA_PATH (installed, but not saved to Settings: see the warning above)"
    fi
}

summary() {
    local port url
    port="${PORT:-$(current_port)}"
    url="http://localhost:$port"
    cat <<EOF

nano-llm-proxy v$VER is installed.

  Admin GUI:            $url
  OpenAI base URL:      $url/v1
  Anthropic base URL:   $url
  API key:              any placeholder (e.g. "local") unless you turn on
                        "Require an API key" in Settings
  Web search:           $SEARCH_STATUS
  obscura:              $OBSCURA_STATUS
  Binary:               $BIN
  Data folder:          $DATADIR

Uninstall:
  curl -fsSL https://github.com/$REPO/releases/latest/download/install.sh | bash -s -- --uninstall
  (add --purge to also delete the data folder)
EOF
}

# --- uninstall ----------------------------------------------------------------

uninstall() {
    if [[ -x $BIN ]]; then
        local args=(local uninstall)
        [[ $PURGE -eq 0 ]] || args+=(--purge)
        say "Removing the background services"
        "$BIN" "${args[@]}" || die "'nano-llm-proxy ${args[*]}' failed; the binary was left in place"
    else
        warn "$BIN not found; no services to remove"
        [[ $PURGE -eq 0 ]] || warn "the data folder was not deleted (it is found by the binary); remove it by hand if it exists"
    fi
    rm -f "$BIN" "$BIN.bak"
    say "Removed $BIN"
    [[ $PURGE -eq 1 ]] || say "Your data folder was kept; re-run with --uninstall --purge to delete it"
}

# --- main -----------------------------------------------------------------------

main() {
    parse_args "$@"
    detect_platform

    if [[ $UNINSTALL -eq 1 ]]; then
        uninstall
        return 0
    fi

    need curl
    need tar
    need awk
    command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 \
        || die "sha256sum or shasum is required to verify the download"
    if [[ $EUID -eq 0 ]]; then
        warn "running as root installs nano-llm-proxy for root only; run it as your normal user instead"
    fi

    TMP=$(mktemp -d "${TMPDIR:-/tmp}/nano-llm-proxy-install.XXXXXX")
    trap 'rm -rf "$TMP"' EXIT

    resolve_release
    download_and_verify
    install_binary

    DATADIR=$("$BIN" local datadir) \
        || die "'nano-llm-proxy local datadir' failed (v$VER may predate local mode)"

    setup_search
    setup_obscura
    run_local_install
    summary
    path_hint
}

# Everything runs from here, so a partly downloaded script does nothing.
main "$@"
