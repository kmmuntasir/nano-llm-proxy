# Security Rules

Source of truth for the public posture is `SECURITY.md`; these rules keep code changes inside it.

## Sacred

- **Never commit or reproduce secrets.** `.env`, `keys.json`, `gateway.db*`, `.secrets.md`, captured traffic with machine details, upstream provider keys, client keys, admin passwords. Never copy their contents into code, commits, docs, tests, or tool output; never send them anywhere.
- **Never log** keys, session tokens, passwords, `Authorization`/`x-api-key` headers, or full request/response bodies. Log labels and ids.
- Fixtures must stay sanitized: `scripts/sanitize-fixtures.py --check` must pass (CI enforces it).

## Trust model (deliberate trade-offs — don't make them worse silently)

- Upstream provider keys are stored **reversibly** in SQLite (they must be forwarded verbatim). `gateway.db` is `0600`. No envelope encryption — if a change affects this, call it out.
- No built-in TLS; binds `127.0.0.1` by default; session cookies are `Secure` by default (`NANO_COOKIE_SECURE`). Behind a proxy, `NANO_TRUSTED_ORIGINS` is the only way to widen Origin acceptance.
- Single instance by design; no shared state across replicas.

## Authentication & authorization

- **Client keys** (`fg-…`): stored **sha256-hashed**; plaintext is shown exactly once at creation. Accepted as `Authorization: Bearer` or `x-api-key` on `/v1/*` and `/mcp` via `clientOnly`. Never store, log, or re-display plaintext.
- **GUI sessions:** cookie-based, bcrypt-verified passwords, `requireSession` on every `/api/*` route except login. Superadmin-only routes also wrap `requireSuperadmin`. Mutating `/api/*` requests are Origin-checked — never loosen it to fix a local problem.
- Login has per-IP + email backoff; don't remove it. Password hashes only (bcrypt via `golang.org/x/crypto`).
- Authorization is scoped by principal: users reach only their own keys/profile/usage; per-user provider revocation hides providers from the catalog and routes them like unknown prefixes. Cross-user access → 403/404.
- UI gating (`role === "superadmin"`) is convenience; the Go middleware is the boundary. A new admin endpoint without `requireSuperadmin` is a bug.

## Input validation

- Bound every body (`readJSON` caps at 1 MiB; use `io.LimitReader` elsewhere). Validate ids with `pathID`. Reject malformed input with a 4xx and a short message — don't echo raw input.
- SQL is parameterized only (`?`). No string-built queries.
- Bootstrap config: malformed values are fatal at boot, never silently defaulted.

## SSRF & web tools

- `web_read` fetches URLs for authenticated users: all outbound fetches go through the `internal/webtools` SSRF guard (blocks private/loopback/link-local, re-checks redirects and resolved IPs). Never add a fetch path that bypasses it.
- Web tools stay metered per user; the MCP endpoint stays behind client-key auth. `scripts/install-web-tools.sh` is separate from the gateway and idempotent.

## Output & GUI

- Never add `dangerouslySetInnerHTML` with untrusted content. Errors shown to users carry the backend's `error.message`, not stack traces or SQL.
- The gateway never returns upstream keys to any client or the GUI.

## Dependencies & supply chain

- Five direct Go deps, all pure Go. A new dependency requires written justification (license, maintenance, why stdlib/local code won't do) and must not need cgo. Same scrutiny for npm packages — they ship inside the binary.
- `go mod tidy` must leave `go.mod`/`go.sum` consistent; no replace directives pointing at local paths.

## Deployment

- `deploy.sh` installs a hardened systemd unit with an unprivileged service user; don't weaken its sandboxing options. Read it before changing it.
- Never put server IPs, SSH paths, or credentials in the repo or docs.

## Reporting

Vulnerabilities go through GitHub private advisories (`SECURITY.md`), never public issues.
