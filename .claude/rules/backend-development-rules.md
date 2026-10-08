# Backend Development Rules

## General

Go (see `go.mod` for the pinned version) + SQLite (`modernc.org/sqlite`, pure Go). Module `github.com/kmmuntasir/nano-llm-proxy`. One static binary (`CGO_ENABLED=0`), GUI embedded under the `prod` build tag. Standard-library `net/http` (`ServeMux` with method+path patterns, `r.PathValue`), `log` for logging, no web framework, no ORM.

**Hard constraints (project identity):** no Docker, ever; sized for a 512 MB VPS (idles ~15 MB); five direct Go dependencies, all pure Go. Adding a dependency needs a written justification — prefer a few lines of local code, then the standard library.

## Project Structure

```
main.go                  # bootstrap wiring only: config → store → gateway → serve; -version, -reset-admin-password
webprod.go / webstub.go  # `prod` tag embeds web/dist; untagged builds return no FS (no Node needed for go test)
internal/config/         # bootstrap env config + .env parser (config.go, dotenv.go)
internal/settings/       # runtime settings document (JSON in the settings table)
internal/store/          # SQLite: schema, in-code migrations, all queries (store.go)
internal/gateway/        # HTTP runtime: routes.go, auth.go, pool.go, provider adapters, SSE, usage, /api/* handlers (api_*.go), MCP (mcp*.go)
internal/webtools/       # web_search (SearXNG) / web_read (fetch + obscura), SSRF guard
scripts/                 # install-web-tools.sh, fixture capture/sanitize
deploy.sh                # build + hardened systemd install
docs/                    # api.md, configuration.md, deployment.md, architecture/
```

`internal/gateway/testdata/` holds captured fixtures — reference material only; no Go test reads it.

## Layering

- `main.go` wires; it holds no logic.
- `store` owns SQL. Handlers never touch `*sql.DB`; they call store methods.
- `gateway` owns HTTP: routing in `routes.go`, one concern per file (`api_users.go`, `api_providers.go`, …). New admin endpoints follow the `api_*.go` neighbors.
- Handlers stay unexported methods on `*gateway`; everything is reachable only through `RegisterRoutes`.
- Pure logic (classification, cooldown math, model-id parsing) stays in small functions that tests call directly.

## Routes & API contract

- Surfaces: `/v1/chat/completions`, `/v1/responses`, `/v1/messages`, `/v1/models`, `POST /mcp` (client-key auth via `clientOnly`); `/health` (open); `/api/*` (GUI backend, session-cookie auth via `requireSession`, superadmin-only via `requireSuperadmin`).
- Register with method-prefixed patterns: `mux.HandleFunc("PATCH /api/users/{id}", g.requireSession(...))`. Parse ids with `pathID`.
- `/api/*` JSON helpers in `api_helpers.go`: `writeJSON`, `readJSON` (1 MiB bounded, writes its own error), `apiErr` → `{"error":{"message":"…"}}`. Do not invent another error shape. `/v1/*` errors follow the shape of the protocol being spoken (OpenAI vs Anthropic) — match the neighboring handler.
- Mirror shapes exactly in `web/src/api/types.ts` when an `/api/*` response changes. Document public-facing endpoint changes in `docs/api.md`.

## Database (SQLite)

- Schema changes ONLY as a new `migrateVn()` in `internal/store/store.go`, applied in one transaction that also inserts the `schema_migrations` row. Never edit an applied migration; never alter schema outside a migration.
- `gateway.db` is created `0600`, WAL mode, source of truth. Memory (pools) is the hot path; every GUI mutation rebuilds pools in the same request — keep that invariant when adding mutations.
- Parameterized queries only (`?` placeholders). Never build SQL from request input.
- Store methods take/return plain structs; keep them testable against `openTestStore(t)` (temp-file DB).

## Settings & Config

- Bootstrap values (port, bind, DB path, cookie flag, trusted origins, admin creds) come from env / `.env` via `internal/config`. Precedence: defaults < `.env` < process env. Malformed values are fatal at boot, never silently ignored.
- Everything tunable at runtime lives in the `settings` table JSON (`internal/settings`) and is edited in the GUI — not new env vars. Omitted fields fall back to defaults; explicit `false`/`0` are honored.
- New bootstrap vars: add to `.env.example` (commented), `docs/configuration.md`.

## Error handling & logging

- Return errors with context (`fmt.Errorf("…: %w", err)`); no swallowed errors. The few intentional ignores carry a comment (`//nolint:errcheck`-style, as in `writeJSON`).
- Log with the standard `log` package. Never log client keys, upstream keys, session tokens, passwords, or full request/response bodies — log identifiers/labels (`key=<label>`), status, and timing.
- Failure classification is a contract: genuine auth failures blacklist a key; rate limits/flakes cool down (honor `Retry-After`); client-shape errors fail fast with a hint; failover stops at the first byte streamed. Preserve these when touching `pool.go` / adapters.

## Concurrency & streaming

- Pools and settings are shared across requests — guard with the existing mutexes; run new tests with `-race`.
- SSE/streaming paths must flush per event and never buffer a whole upstream response (memory budget). Non-streaming clients get aggregated responses.
- Always bound outbound I/O: contexts with timeouts, `io.LimitReader` on bodies you parse.

## Provider adapters

Provider = preset or custom URL; model prefix selects the provider and is stripped before the upstream call. Adapters: built-in `zen`, `generic` (any OpenAI/Anthropic-compatible URL), presets in `providerspec.go`. Quirks go in the adapter, not in handlers. Catalog metadata (context window, max output, modalities, reasoning) is published under every spelling clients read — keep new fields consistent across `/v1/models` variants (see `docs/api.md`).

## Build and Run

```bash
go run .                                                   # gateway on :8787 (needs ADMIN_EMAIL/ADMIN_PASSWORD on first boot)
(cd web && npm ci && npm run build)                        # build the GUI (embedded under `prod`)
CGO_ENABLED=0 go build -tags prod -trimpath -ldflags="-s -w" -o nano-llm-proxy .
go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"   # the gate (matches CI)
sudo ./deploy.sh                                           # build + hardened systemd unit (read deploy.sh and docs/deployment.md first)
```

## Avoid

- Docker, containers, compose files, cgo, new runtime services.
- New Go dependencies without justification; web frameworks/ORMs.
- Editing an applied migration; schema changes outside `migrateVn()`.
- String-built SQL; logging secrets or payloads; widening the SSRF/Origin/auth surface.
- Tests that need a network, a live provider, API keys, or Node.
- Committing `.env`, `keys.json`, `gateway.db*`, or anything from `.secrets.md`.
