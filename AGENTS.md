# AGENTS.md

nano-llm-proxy: a single-binary LLM gateway (Go + SQLite) with an embedded
admin GUI (`web/`, React). No Docker, by design.

## Deployment

When the user asks to deploy, update, or restart a deployment:

1. Read `deploy.sh` first — it is the deployment script. It builds the GUI
   and binary and installs a systemd service; follow it rather than
   inventing alternative steps.
2. Read `docs/deployment.md` for the full walkthrough: first-boot
   requirements, manual and remote-update flows, and post-deploy checks.

The optional MCP web-tool backends (SearXNG + obscura) are a separate,
idempotent installer: `scripts/install-web-tools.sh` (use `--check` for a
read-only verify). It never touches the gateway binary; `deploy.sh` does
not install it.

## Local install (one person's computer)

When the user wants nano-llm-proxy on their own machine rather than a
server, use local mode instead of `deploy.sh`: the one-line installers in
`scripts/install/` (published as release assets) or
`nano-llm-proxy local install`. Read `docs/local.md` first. Local mode has
no sign-in and listens on loopback only; never point it at a shared or
multi-user machine.

`.env`, `keys.json`, and `gateway.db` hold secrets and runtime state — never
commit them, copy their contents into code or commits, or send them anywhere.
