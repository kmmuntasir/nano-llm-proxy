#!/usr/bin/env bash
# nano-llm-proxy deployment script — builds the single binary and sets it up
# as a systemd service on this machine. No Docker; the runtime footprint is
# one static binary plus a SQLite file, sized for small (512 MB) VPS boxes.
#
# Usage:  sudo ./deploy.sh            (from the repository root)
#
# Requires a .env file in the repository root (see .env.example). The script
# refuses to run without one, and copies it next to the binary so the service
# picks it up via EnvironmentFile.

set -euo pipefail

APP_NAME="nano-llm-proxy"
APP_DIR="${NANO_DEPLOY_DIR:-/opt/nano-llm-proxy}"
SERVICE_USER="${NANO_SERVICE_USER:-nano}"
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# --- preflight --------------------------------------------------------------

[[ $EUID -eq 0 ]] || die "run as root (sudo ./deploy.sh)"

ENV_FILE="$REPO_DIR/.env"
[[ -f "$ENV_FILE" ]] || die ".env not found in $REPO_DIR — copy .env.example to .env and fill it in first"
grep -q '^ADMIN_EMAIL=.\+' "$ENV_FILE" || die ".env is missing a non-empty ADMIN_EMAIL (first boot needs it)"
grep -q '^ADMIN_PASSWORD=.\+' "$ENV_FILE" || die ".env is missing a non-empty ADMIN_PASSWORD (first boot needs it)"

command -v go >/dev/null || die "go toolchain not found on PATH"
command -v npm >/dev/null || die "npm not found on PATH"
command -v systemctl >/dev/null || die "systemctl not found — this script targets systemd hosts"

# --- build ------------------------------------------------------------------

log "building admin GUI"
(cd "$REPO_DIR/web" && npm ci --silent && npm run build --silent)

log "building $APP_NAME binary"
(cd "$REPO_DIR" && CGO_ENABLED=0 go build -tags prod -trimpath -ldflags="-s -w" -o "$APP_NAME" .)

# --- install ----------------------------------------------------------------

log "installing to $APP_DIR"
mkdir -p "$APP_DIR"
install -m 0755 "$REPO_DIR/$APP_NAME" "$APP_DIR/$APP_NAME"
install -m 0600 "$ENV_FILE" "$APP_DIR/.env"

# dedicated unprivileged service user; existing user is reused as-is
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
    log "creating service user $SERVICE_USER"
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi
chown "$SERVICE_USER:$SERVICE_USER" "$APP_DIR"
chown "$SERVICE_USER:$SERVICE_USER" "$APP_DIR/.env"

# --- systemd unit -----------------------------------------------------------

log "writing systemd unit"
cat > "/etc/systemd/system/$APP_NAME.service" <<EOF
[Unit]
Description=nano-llm-proxy — single-binary LLM gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$APP_DIR
EnvironmentFile=$APP_DIR/.env
ExecStart=$APP_DIR/$APP_NAME
Restart=on-failure
RestartSec=3
# the DB lives in APP_DIR; everything else stays read-only
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=$APP_DIR

[Install]
WantedBy=multi-user.target
EOF

log "enabling and restarting $APP_NAME"
systemctl daemon-reload
systemctl enable "$APP_NAME" >/dev/null
systemctl restart "$APP_NAME"

sleep 1
systemctl --no-pager --lines 5 status "$APP_NAME" || true
log "done — journal: journalctl -u $APP_NAME -f"
log "optional MCP web tools (SearXNG + obscura, separate services): sudo ./scripts/install-web-tools.sh"
