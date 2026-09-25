#!/usr/bin/env bash
# install-web-tools.sh — install/update/check the self-hosted backends for
# nano-llm-proxy's /mcp web tools:
#
#   SearXNG (search)  — bare-metal install under /opt/searxng, systemd unit
#                       running the built-in server on 127.0.0.1:8888, JSON
#                       API enabled, trimmed no-API-key engine list.
#   obscura  (reader) — prebuilt release binaries into /usr/local/bin.
#
# Idempotent: every step converges, re-running is always safe. No Docker.
# Sized for small (512 MB) hosts: SearXNG gets MemoryMax=280M and runs the
# built-in server (≈ uwsgi with 1 worker, fewer moving parts).
#
# Usage:
#   sudo ./scripts/install-web-tools.sh                # install both
#   sudo ./scripts/install-web-tools.sh --update       # bump to pinned refs + restart
#   ./scripts/install-web-tools.sh --check             # verify (no root, no writes)
#   sudo ./scripts/install-web-tools.sh --skip-searxng # obscura only
#   sudo ./scripts/install-web-tools.sh --obscura-dir ~/obscura   # offline binaries
#
# Pins (env-overridable): SEARXNG_REF, OBSCURA_VERSION

set -euo pipefail

log() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE_DIR="$REPO_DIR/scripts/templates"

# --- pins -------------------------------------------------------------------
# SearXNG publishes no release tags; pin a master commit. Bump via --update
# after checking https://github.com/searxng/searxng/commits/master .
SEARXNG_REF="${SEARXNG_REF:-d8ae3abd59b6cb3970e3380e484c7d4cd8529619}"
OBSCURA_VERSION="${OBSCURA_VERSION:-v0.2.3}"
OBSCURA_REPO="https://github.com/h4ckf0r0day/obscura"
SEARXNG_REPO="https://github.com/searxng/searxng.git"

SEARXNG_HOME="/opt/searxng"
SEARXNG_APP="$SEARXNG_HOME/app"
SEARXNG_VENV="$SEARXNG_HOME/venv"
SEARXNG_USER="searxng"
SEARXNG_SETTINGS="/etc/searxng/settings.yml"
SEARXNG_UNIT="/etc/systemd/system/searxng.service"
SEARXNG_PORT=8888

BIN_DIR="/usr/local/bin"

# --- flags ------------------------------------------------------------------
MODE="install"
SKIP_SEARXNG=0
SKIP_OBSCURA=0
OBSCURA_DIR=""
ASSUME_YES=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --check) MODE="check" ;;
        --update) MODE="update" ;;
        --skip-searxng) SKIP_SEARXNG=1 ;;
        --skip-obscura) SKIP_OBSCURA=1 ;;
        --obscura-dir) OBSCURA_DIR="${2:?}"; shift ;;
        --searxng-ref) SEARXNG_REF="${2:?}"; shift ;;
        --obscura-version) OBSCURA_VERSION="${2:?}"; shift ;;
        --yes|-y) ASSUME_YES=1 ;;
        -h|--help)
            sed -n '2,20p' "$0"; exit 0 ;;
        *) die "unknown flag: $1 (see --help)" ;;
    esac
    shift
done

# --- check mode (read-only, no root) ----------------------------------------

check_http() { # url -> body on stdout (untruncated: callers json.load it)
    curl -fsS --max-time 8 "$1" 2>/dev/null
}

check_http_timeout() { # timeout url  -> same, custom curl budget
    local t="$1"; shift
    curl -fsS --max-time "$t" "$1" 2>/dev/null
}

cmd_check() {
    local fail=0

    # 1. SearXNG (45 s budget: the first search on a cold instance takes
    # 30 s+ while engines warm up — an 8 s probe false-fails)
    printf '%-28s' "SearXNG (127.0.0.1:$SEARXNG_PORT)"
    local body
    if body=$(check_http_timeout 45 "http://127.0.0.1:$SEARXNG_PORT/search?q=searxng&format=json"); then
        if printf '%s' "$body" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert isinstance(d.get("results"), list)' 2>/dev/null; then
            echo "OK (JSON results present)"
        else
            echo "FAIL — responded but not JSON with results"
            warn "is \"json\" listed under search: formats: in $SEARXNG_SETTINGS?"
            fail=1
        fi
    else
        echo "FAIL — unreachable"
        systemctl is-active --quiet searxng 2>/dev/null \
            && warn "service searxng is active but not answering — journalctl -u searxng -n 30" \
            || warn "service not active — sudo ./scripts/install-web-tools.sh --skip-obscura"
        fail=1
    fi

    # 2. obscura
    printf '%-28s' "obscura ($BIN_DIR/obscura)"
    if command -v obscura >/dev/null 2>&1 || [[ -x "$BIN_DIR/obscura" ]]; then
        local ver
        ver=$("$BIN_DIR/obscura" --version 2>/dev/null || obscura --version 2>/dev/null || echo "")
        if [[ -n "$ver" ]]; then
            echo "OK ($ver)"
        else
            echo "FAIL — binary present but not runnable"
            fail=1
        fi
    else
        echo "FAIL — not found"
        warn "sudo ./scripts/install-web-tools.sh --skip-searxng  (or --obscura-dir with local binaries)"
        fail=1
    fi

    # 3. gateway health + webtools flag
    local gw_port="${NANO_GATEWAY_URL:-http://127.0.0.1:8787}"
    printf '%-28s' "gateway ($gw_port)"
    if body=$(check_http "$gw_port/health"); then
        if printf '%s' "$body" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("status")=="ok"' 2>/dev/null; then
            if printf '%s' "$body" | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin).get("webtools",{}).get("enabled") else 1)' 2>/dev/null; then
                echo "OK (web tools enabled)"
            else
                echo "WARN — gateway up, web tools disabled"
                warn "enable them in the admin GUI → Settings → Web tools"
            fi
        else
            echo "FAIL — /health did not return status ok"
            fail=1
        fi
    else
        echo "FAIL — gateway unreachable"
        warn "is nano-llm-proxy running? set NANO_GATEWAY_URL if not on 8787"
        fail=1
    fi

    if [[ $fail -ne 0 ]]; then
        die "one or more checks failed"
    fi
    log "all checks passed"
}

# --- install/update modes (root) --------------------------------------------

if [[ $MODE == check ]]; then
    cmd_check
    exit 0
fi

[[ $EUID -eq 0 ]] || die "run as root (sudo ./scripts/install-web-tools.sh) — use --check without root"
command -v systemctl >/dev/null || die "systemctl not found — this script targets systemd hosts"

install_searxng_deps() {
    if command -v apt-get >/dev/null 2>&1; then
        log "installing system packages (apt)"
        export DEBIAN_FRONTEND=noninteractive
        # ForceIPv4: containers with no IPv6 route hang on v6-preferring
        # mirrors; v4 always works where apt works at all.
        apt-get update -qq -o Acquire::ForceIPv4=true
        apt-get install -y -qq --no-install-recommends -o Acquire::ForceIPv4=true \
            python3 python3-venv python3-dev git gcc libxml2-dev libxslt1-dev curl ca-certificates
    elif command -v dnf >/dev/null 2>&1; then
        log "installing system packages (dnf)"
        dnf install -y python3 python3-devel git gcc libxml2-devel libxslt1-devel curl ca-certificates
    elif command -v pacman >/dev/null 2>&1; then
        log "installing system packages (pacman)"
        pacman -Sy --needed --noconfirm python python-virtualenv git gcc libxml2 libxslt curl
    else
        die "no apt/dnf/pacman found — install python3-venv, git, gcc, libxml2/libxslt dev headers manually"
    fi
}

searxng_at_ref() { # true when the clone exists at exactly $SEARXNG_REF
    [[ -d "$SEARXNG_APP/.git" ]] \
        && git -C "$SEARXNG_APP" rev-parse HEAD 2>/dev/null | grep -q "^$SEARXNG_REF"
}

install_searxng() {
    install_searxng_deps

    log "ensuring $SEARXNG_USER service user"
    id "$SEARXNG_USER" >/dev/null 2>&1 || \
        useradd --system --home-dir "$SEARXNG_HOME" --shell /usr/sbin/nologin "$SEARXNG_USER"
    mkdir -p "$SEARXNG_HOME"
    chown "$SEARXNG_USER:$SEARXNG_USER" "$SEARXNG_HOME"

    log "fetching SearXNG at ${SEARXNG_REF:0:12}"
    if [[ ! -d "$SEARXNG_APP" ]]; then
        mkdir -p "$SEARXNG_APP"
        git clone --quiet --no-checkout "$SEARXNG_REPO" "$SEARXNG_APP"
    fi
    # safe.directory is only honored from PROTECTED config (system/global),
    # never the repo's local config — --local here would be a silent no-op.
    # Root needs it: the clone ends up owned by the searxng user, and git
    # refuses to operate on it otherwise on every later run.
    git config --global --add safe.directory "$SEARXNG_APP" 2>/dev/null || true
    if ! searxng_at_ref; then
        git -C "$SEARXNG_APP" fetch --depth 1 origin "$SEARXNG_REF"
        git -C "$SEARXNG_APP" checkout --force FETCH_HEAD
    fi
    # a --no-checkout clone whose checkout silently no-ops would fail pip
    # with a baffling "not a Python project" — fail loudly here instead
    # (upstream currently ships setup.py; accept pyproject.toml in case it moves)
    [[ -f "$SEARXNG_APP/setup.py" || -f "$SEARXNG_APP/pyproject.toml" ]] \
        || die "SearXNG checkout incomplete at $SEARXNG_APP — remove $SEARXNG_APP and re-run"
    chown -R "$SEARXNG_USER:$SEARXNG_USER" "$SEARXNG_APP"

    local upgrade_flag=""
    [[ $MODE == update ]] && upgrade_flag="--upgrade"

    log "building virtualenv + dependencies (a few minutes on first run)"
    sudo -u "$SEARXNG_USER" python3 -m venv "$SEARXNG_VENV"
    # --no-build-isolation needs the build deps in the venv itself: SearXNG's
    # setup.py imports msgspec; Ubuntu 24.04 venvs ship without setuptools.
    # Same set the upstream bare-metal docs pre-install.
    sudo -u "$SEARXNG_USER" "$SEARXNG_VENV/bin/pip" install --quiet $upgrade_flag \
        setuptools wheel pyyaml msgspec typing-extensions pybind11
    sudo -u "$SEARXNG_USER" "$SEARXNG_VENV/bin/pip" install --quiet --use-pep517 \
        --no-build-isolation $upgrade_flag -e "$SEARXNG_APP"

    log "writing settings"
    if [[ -f "$SEARXNG_SETTINGS" ]]; then
        warn "$SEARXNG_SETTINGS already exists — leaving it untouched (secret + edits preserved)"
    else
        mkdir -p /etc/searxng
        local secret
        secret=$(openssl rand -hex 32)
        sed "s/@@SEARXNG_SECRET@@/$secret/" "$TEMPLATE_DIR/searxng-settings.yml" > /etc/searxng/settings.yml
        chown root:"$SEARXNG_USER" /etc/searxng/settings.yml
        chmod 0640 /etc/searxng/settings.yml
    fi

    log "writing systemd unit"
    local unit_new="$SEARXNG_UNIT.new" unit_changed=0
    sed "s|@@SEARXNG_VENV@@|$SEARXNG_VENV|" "$TEMPLATE_DIR/searxng.service" > "$unit_new"
    if ! cmp -s "$unit_new" "$SEARXNG_UNIT"; then
        mv "$unit_new" "$SEARXNG_UNIT"
        chmod 0644 "$SEARXNG_UNIT"
        unit_changed=1
    else
        rm -f "$unit_new"
    fi
    systemctl daemon-reload
    systemctl enable --quiet searxng

    # restart only when needed: a restart cold-starts the engines, and the
    # first queries on a cold instance are slow (upstream CAPTCHAs, timeouts)
    if [[ $unit_changed -eq 1 || $MODE == update ]] || ! systemctl is-active --quiet searxng; then
        log "starting searxng"
        systemctl restart searxng
    fi

    log "waiting for SearXNG readiness"
    local ok=0
    for _ in $(seq 1 30); do
        # /config is served without touching search engines — a pure
        # readiness signal (a fresh search query can take 30s+ cold)
        if check_http "http://127.0.0.1:$SEARXNG_PORT/config" \
            | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
            ok=1
            break
        fi
        sleep 2
    done
    if [[ $ok -ne 1 ]]; then
        systemctl --no-pager --lines 10 status searxng || true
        warn "last journal lines:"
        journalctl -u searxng -n 20 --no-pager || true
        warn "is anything already on port $SEARXNG_PORT? ss -ltnp | grep $SEARXNG_PORT"
        die "SearXNG did not become ready — fix and re-run (safe to re-run)"
    fi

    log "running one real search (first query on a cold instance can take ~30 s)"
    if ! check_http_timeout 60 "http://127.0.0.1:$SEARXNG_PORT/search?q=searxng&format=json" \
        | python3 -c 'import json,sys; assert isinstance(json.load(sys.stdin).get("results"), list)' 2>/dev/null; then
        warn "SearXNG is up but the sample search returned no usable JSON — engines may be CAPTCHA-blocked from this IP"
        warn "see docs/deployment.md (engine trimming) and check: curl 'http://127.0.0.1:$SEARXNG_PORT/search?q=test&format=json'"
    else
        log "SearXNG healthy on 127.0.0.1:$SEARXNG_PORT"
    fi
}

obscura_arch() {
    case "$(uname -m)" in
        x86_64) echo "x86_64" ;;
        aarch64|arm64) echo "aarch64" ;;
        *) die "unsupported architecture: $(uname -m)" ;;
    esac
}

install_obscura() {
    local arch
    arch=$(obscura_arch)
    local tmp
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' RETURN

    if [[ -n "$OBSCURA_DIR" ]]; then
        log "installing obscura from $OBSCURA_DIR (offline)"
        [[ -x "$OBSCURA_DIR/obscura" ]] || die "no obscura binary in $OBSCURA_DIR"
        [[ -x "$OBSCURA_DIR/obscura-worker" ]] || die "no obscura-worker binary in $OBSCURA_DIR"
        install -m 0755 "$OBSCURA_DIR/obscura" "$tmp/obscura"
        install -m 0755 "$OBSCURA_DIR/obscura-worker" "$tmp/obscura-worker"
    else
        log "downloading obscura $OBSCURA_VERSION ($arch, stealth build)"
        local url="$OBSCURA_REPO/releases/download/$OBSCURA_VERSION/obscura-$arch-linux-stealth.tar.gz"
        curl -fsSL --retry 3 -o "$tmp/obscura.tar.gz" "$url" \
            || die "download failed: $url (offline? use --obscura-dir with local binaries)"
        tar -xzf "$tmp/obscura.tar.gz" -C "$tmp"
        [[ -x "$tmp/obscura" ]] || die "archive did not contain an obscura binary"
        [[ -x "$tmp/obscura-worker" ]] || warn "archive did not contain obscura-worker"
    fi

    # atomic replace: install to .new then mv, so a running fetch is unaffected
    install -m 0755 "$tmp/obscura" "$BIN_DIR/obscura.new"
    mv -f "$BIN_DIR/obscura.new" "$BIN_DIR/obscura"
    if [[ -x "$tmp/obscura-worker" ]]; then
        install -m 0755 "$tmp/obscura-worker" "$BIN_DIR/obscura-worker.new"
        mv -f "$BIN_DIR/obscura-worker.new" "$BIN_DIR/obscura-worker"
    fi

    log "obscura $($BIN_DIR/obscura --version 2>/dev/null | head -1) installed to $BIN_DIR"
}

main() {
    if [[ $MODE == update ]]; then
        [[ $ASSUME_YES -eq 1 ]] || {
            read -r -p "Update SearXNG to ${SEARXNG_REF:0:12} and obscura to $OBSCURA_VERSION? [y/N] " a
            [[ ${a,,} == y* ]] || die "aborted"
        }
    fi

    [[ $SKIP_SEARXNG -eq 1 ]] || install_searxng
    [[ $SKIP_OBSCURA -eq 1 ]] || install_obscura

    log "done. Next steps:"
    echo "    1. open the admin GUI → Settings → Web tools → enable + Test SearXNG / Test obscura"
    echo "       (no gateway restart is needed: obscura is exec'd per fetch, settings hot-reload)"
    echo "    2. re-verify any time with: ./scripts/install-web-tools.sh --check"
}

main
