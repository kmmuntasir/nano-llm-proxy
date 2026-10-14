# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Release notes are generated from commit prefixes, so `feat:`, `fix:`,
`security:`, `ci:`, `docs:`, and `test:` all matter.

## [Unreleased]

### Added

- Local mode: run nano-llm-proxy on your own computer for yourself alone
  (`nano-llm-proxy local`, or `NANO_MODE=local`). No sign-in — the GUI runs
  as a single implicit owner and the Users page, roles, password and logout
  are hidden; agents can call `/v1/*` and `/mcp` with any placeholder key,
  billed to a built-in `local (no key)` client key, unless the new
  `local.requireApiKey` setting ("Require an API key") is on. Data lives in
  a per-user folder (`~/.local/share/nano-llm-proxy`,
  `~/Library/Application Support/nano-llm-proxy`,
  `%LOCALAPPDATA%\nano-llm-proxy`). With no login, local mode only binds
  loopback (any other `NANO_BIND` is fatal), checks the `Host` header on
  every route against DNS rebinding, and refuses foreign `Origin`s (403) and
  non-JSON `POST`s (415) on `/v1/*` and `/mcp`. A Settings → This computer
  card toggles start at sign-in, changes the port, and shows the data
  folder, log file and SearXNG status, backed by `GET /api/local`,
  `PUT /api/local/autostart` and `PUT /api/local/port` (404 in server mode).
  `GET /api/auth/config` now always reports `mode` (`server` or `local`).
  The in-app Guide follows the mode: placeholder-key snippets (real `fg-`
  keys once "Require an API key" is on), no admin or account wording, and
  local troubleshooting. See `docs/local.md`.
- One-line installers for local mode, attached to every release:
  `install.sh` (Linux, macOS), `install.ps1` (Windows PowerShell) and
  `install.cmd` (Windows CMD). No admin rights: they verify the download
  against `checksums.txt`, install to your home folder, register a per-user
  background service that starts at sign-in (systemd user unit, XDG
  autostart fallback, launchd LaunchAgent, or the `HKCU` Run key with the
  new console-less `nano-llm-proxyw.exe`), and open the GUI. On Linux and
  macOS they also set up a private SearXNG for web search on
  `127.0.0.1:8899` (needs Python 3.10+ and git). Re-running upgrades;
  `--uninstall [--purge]` removes it. New `nano-llm-proxy local install`,
  `uninstall [--purge]`, `status` and `datadir` subcommands.

### Fixed

- The Windows release zip shipped `nano-llm-proxy.exe.exe`; it now holds
  `nano-llm-proxy.exe`.
- The SearXNG settings template never actually trimmed the engine list:
  `use_default_settings: true` kept all 80+ default engines and the listed
  ones were only re-enabled. It now uses `use_default_settings.engines.keep_only`,
  lists `stackoverflow` (there is no `stackexchange` engine), and drops
  startpage, mojeek and marginalia, which the pinned SearXNG marks inactive.
  `install-web-tools.sh` never overwrites an existing
  `/etc/searxng/settings.yml`, so existing server installs keep the full
  list — replace `use_default_settings: true` there with the `keep_only`
  block from `scripts/templates/searxng-settings.yml` and restart SearXNG.

## [0.2.0] — 2026-10-14

A large feature release: roles and Google sign-in for teams, a richer
Models page with benchmarks and comparison, daily key rotation, upstream
key verification, Zen and Pi compatibility fixes, and an About page with
this changelog.

**Upgrading from 0.1.x:** migrations V8–V10 run automatically on first
start (back up `gateway.db` first). The admin endpoints for managing other
users' client keys (`POST /api/users/{id}/keys`, `PATCH`/`DELETE
/api/users/{id}/keys/{keyId}`) are gone — keys are managed by their owner.
The built-in zen provider moves to `https://opencode.ai/inference/v1` unless
its base URL was customised. Existing deployments keep their stored
rotation mode; only fresh installs default to `priority-daily`.

### Added

- Version and About page. The sidebar shows the running version (e.g.
  `v0.1.1+48`); clicking it opens an About page with the build details, the
  GitHub links and an invitation to contribute, and the changelog of every
  release as an accordion, newest first and expanded. The changelog is
  `CHANGELOG.md` embedded in the binary and served by `GET /api/about`, so
  it always matches the running build.
- Admin role. Roles are now `superadmin`, `admin` and `user`. Admins have
  every admin page and capability; the one difference is that they can't
  change a superadmin account in any way (deactivate, delete, rename, change
  role, reset password) and can't grant the `superadmin` role. Superadmins
  can change other superadmins; nobody can deactivate or delete their own
  account. Migration V9 rebuilds the `users` table to widen its role
  constraint (keeping the id high-water mark, so deleted users' ids are never
  reused).
- Google sign-in for the GUI (`GOOGLE_CLIENT_ID`, optionally limited by
  `GOOGLE_WORKSPACE_DOMAIN`). The browser gets a Google-signed ID token and
  the gateway verifies it locally (RS256 against Google's keys, issuer,
  audience, expiry, verified email, Workspace domain) — no client secret.
  Google sign-in never creates accounts: only users an admin added on the
  Users page can use it. New endpoints `GET /api/auth/config` (open) and
  `POST /api/auth/google`. Password sign-in becomes a runtime setting
  (`auth.credentialLogin`, default on) that can only be turned off when
  Google is configured and by an admin who could sign back in with Google.
- Self-service profile: `PATCH /api/me` renames a password user. Accounts
  that have signed in with Google are Google-owned (`googleLinked`): their
  name and profile picture (`avatarUrl`, Google-hosted only) refresh on every
  Google sign-in, can't be edited by anyone, and they can't change a
  password. Migration V8 adds both columns. Avatars (or initials) show in
  the sidebar, the profile page and the users list.
- Model benchmarks on the Models page: Artificial Analysis Intelligence /
  Coding / Agentic indexes and Design Arena results from OpenRouter's public
  catalog, fetched with the models.dev sync and joined to catalog entries by
  a normalized model slug. `/api/models` entries gain `benchmarks` and the
  response `benchmarks_status`; `/v1/models` is unchanged. The page gains a
  details dialog, a compare view for up to three models, sorting by score or
  context window, and a "has benchmarks" filter.
- `priority-daily` rotation (now the default): priority's stick-with-failover
  from a starting key that advances one position per day, per provider.
- Default providers for new users: the `newUsers.disabledProviderIds`
  runtime setting lists providers a newly created `user`-role account starts
  without (written as per-user revocations together with the account).
  Existing users are not affected.
- Upstream key verification before save. Every new key — added to a
  provider or among a new provider's initial keys — is probed against each
  endpoint the provider has (the model list plus a one-token call for a
  nonexistent model; OpenRouter also `GET /api/v1/key`), so no inference
  runs and no quota is spent. A 401, or a 400 that says the key is wrong
  (xAI's answer), rejects it as invalid; a 403 (except zen's
  `FreeTierError`) rejects it as refused — possibly genuine, but unusable
  from this server (region, IP allowlist, permissions, blocked account or
  budget); an unreachable upstream rejects it with a 503. Upstream text is
  shown with the key redacted; pasted keys are trimmed.
- Users page: an Edit dialog (name — locked for Google-linked users — role,
  and an optional new password, replacing the separate reset dialog), role
  badges, and a redesigned card. Activating/deactivating a user, activating/
  deactivating a provider, and saving settings now ask for confirmation (the
  settings dialog lists which sections changed and warns before password
  sign-in is turned off). Every modal has a blurred backdrop.
- Cached-input-token tracking. Every adapter now taps the cache-hit count
  upstreams report (chat `prompt_tokens_details.cached_tokens`, Responses
  `input_tokens_details.cached_tokens`, Anthropic `cache_read_input_tokens`)
  into a new `usage_events.cached_tokens` column (migration V7; older rows
  read 0). `inputTokens` stays the total prompt with `cachedTokens` its
  cache-read subset; native Anthropic usage is normalized to that meaning
  (input + cache_read + cache_creation). `/api/usage/overview` (totals and
  points), `/breakdown` (rows and totals) and `/requests` rows gain
  `cachedTokens`; breakdown and requests accept `sort=cachedTokens`, series
  accepts `metric=cachedTokens`. The v1 `/api/usage/summary` family is
  unchanged.
- Usage monitoring v2 (backend). `usage_events` gains per-request detail
  (migration V6): client surface, stream flag, upstream attempts, last
  upstream status, serving upstream key label, time to first upstream byte,
  the fail hint (≤ 300 chars), and email/alias snapshots so rows stay
  labelled after a user or key is deleted. Requests rejected before routing
  (bad model string, unknown/revoked provider) are now recorded with their
  4xx status. New session endpoints `GET /api/usage/overview`, `/breakdown`
  (grouped, paged, sortable, searchable), `/series` (top-N + other),
  `/requests` (raw log) and `/facets`, with strict parameter validation and
  per-user scoping; cross-user groupings are admin-only.
- Usage monitoring v2 (admin GUI). The Usage page becomes a tabbed section —
  Overview, Users, Models, Users × Models, Requests — with user and model
  drill-down pages, a shared filter bar (range presets, user, provider,
  model, client key, surface, status) kept in the URL, server-side paginated
  and sortable tables, and charts for requests/errors, tokens, latency and
  top-N series. Users and Users × Models are admin-only.
- `/v1/models` entries now carry `context_length` alongside
  `context_window` (same value — different agents read different spellings,
  and a missing field is read as "unknown, assume 128K").
- `/v1/models` reasoning models now carry `reasoning_options`, the
  models.dev-shaped control list (`{"type":"effort","values":["low","medium","high"]}`,
  `{"type":"toggle"}`, `{"type":"budget_tokens","min":…,"max":…}`). The
  ladders are read from models.dev by the existing catalog sync and stored per
  model in `zen.modelMeta` / `zai.modelMeta`; reasoning models the catalog
  doesn't document yet fall back to `effort` with `low`/`medium`/`high`, and
  non-reasoning models never get the field. This is what oh-my-pi needs
  beyond the `reasoning` boolean.
- Kilo preset providers get `reasoning` + `reasoning_options` from the same
  sync, stored in the new `kilo.modelMeta` and keyed by Kilo's own ids
  (`:free` suffix included, which models.dev's kilo entry carries too).
  Upstream's context/output limits, modalities and `supported_parameters`
  still win — only the reasoning facts are resolved. Models models.dev
  doesn't document keep both fields absent instead of an unverified `false`.
- Catalog entries now also publish their input modalities as `input` (what
  pi-openai-compat, i.e. pi/oh-my-pi, reads) and
  `architecture.input_modalities` (what pi's opencode provider loader reads)
  alongside the existing `input_modalities`. Without the pi spelling an
  image-capable model read as text-only and the agent dropped pictures
  silently. Custom providers' modalities are mapped too, so a model behind
  one no longer looks text-only.
- Catalog entries now carry a nested `limits` object
  (`max_input_tokens` / `max_output_tokens`). Clients that read the output
  budget only from there — oh-my-pi's model discovery among them — showed
  every model as 33K max-out because it falls back to that default when the
  object is absent, while reading context from the top-level field. Both
  numbers come from the same enriched values as `context_window` and
  `max_output_tokens`; entries with no advertised output limit omit the
  object instead of claiming zero.

### Changed

- **Breaking:** admins can no longer create, toggle or delete another
  user's client keys — `POST /api/users/{id}/keys` and
  `PATCH/DELETE /api/users/{id}/keys/{keyId}` are gone. Keys are managed by
  their owner only (`/api/me/keys`); admins get a read-only view
  (`GET /api/users/{id}/keys`), and deactivating a user turns all of their
  keys off.
- Superadmins and admins always have every provider: per-user provider
  access applies to regular users only (stray revocations for admins are
  ignored, and promotion clears a user's revocations).
- The gateway never answers 502. Cloudflare in front of a deployment
  replaces an origin's 502 body with its own "Bad gateway" page, hiding the
  reason. A request the upstream refused as sent is now a 400
  (`upstream 4xx: …`); key exhaustion, outages and gate rejections — and a
  catalog with every provider down, Google unreachable, or a per-key usage
  or per-provider catalog fetch failing — are a 503 with the gateway's hint.
  Error bodies on `/v1/messages` use `invalid_request_error` for a 400.
- The built-in zen provider uses opencode's `https://opencode.ai/inference/v1`
  (migration V10 moves it only if it still holds the old default), and the
  default `zen.userAgent` is `opencode/latest/2.0.29/cli`; the 1.18.0 floor
  check accepts both the 1.x `opencode/X.Y.Z` and the 2.x
  `opencode/<channel>/X.Y.Z/<client>` shapes. The zen gate tool stubs are
  `shell`/`read`/`edit` (the opencode 2.x names) and carry opencode's real
  parameter schemas.
- Translated `/v1/messages` responses report usage in Anthropic semantics:
  `input_tokens` excludes cache hits, `cache_read_input_tokens` carries them
  and `cache_creation_input_tokens` is 0, so clients summing the three get
  the upstream's total prompt. `message_delta` now carries the full usage
  block (previously only `output_tokens`). Chat responses translated from
  Responses or Anthropic upstreams gain
  `prompt_tokens_details.cached_tokens` (and `total_tokens`).

### Fixed

- zen free models work again. Zen's free-tier gate tightened twice: since
  2026-10-12 it validates `x-opencode-session` as a real opencode
  identifier (the gateway now mints opencode-encoded session ids and sends
  the full opencode 2.x fingerprint — `x-opencode-client`,
  `x-opencode-project`, the session id under all four session headers,
  `traceparent`/`b3`), and since 2026-10-14 it requires `shell`/`read`
  tools with non-empty parameter schemas (the stubs now carry opencode
  2.0.29's real ones). Both drew `403 FreeTierError` on every request.
- The `developer` instruction role is renamed to `system` for every
  upstream except OpenAI. pi / oh-my-pi send it for reasoning models; zen
  models behind a strict validator answered `unknown variant 'developer'`
  and Z.ai answers `1214 Incorrect role information`.
- Image input works on every path: the chat → Responses retyping now unwraps
  chat's nested `{"image_url":{"url":…}}` into the string the Responses API
  wants (muse-spark and friends used to reject the message with
  `input[0].content did not match any supported type`), and Anthropic image
  blocks on `/v1/messages` (how Claude Code sends pictures) become chat
  `image_url` parts instead of being dropped silently — the model used to
  answer about a picture it had never seen.
- Deactivating a user now also turns their client keys off on `/v1/*`;
  previously only their GUI sessions were blocked.
- The same upstream key can no longer be added to a provider twice (409),
  nor appear twice among a new provider's initial keys (400); a duplicate
  got double weight in key rotation.
- Key cooldown logs show the upstream status (`HTTP 429`) instead of
  `HTTP 0`.
- Untagged builds (`go build` without `-tags prod`) no longer panic at
  startup on a duplicate `/` route; they serve the GUI-not-built stub.
- `/api/dashboard` no longer shows every user's recent requests to every
  logged-in user. Non-admins now get only their own activity entries
  (upstream key hash blanked) and pool health counts without per-key views.
- Client-facing errors for a rejected upstream key no longer include the
  key's label.
- Usage date ranges reject negative bounds and clamp `to` to now; extreme
  values could previously hang or exhaust memory in the bucketed endpoints.

## [0.1.1] — 2026-09-27

Fixes to the first release, both found by downloading the v0.1.0 artifact and
following its own instructions.

### Fixed

- The binary inside the release archive is named `nano-llm-proxy` rather than
  `nano-llm-proxy-<os>-<arch>`, so the documented `./nano-llm-proxy` works
  after extracting.
- A fresh install no longer requires a legacy `keys.json` to boot. The gateway
  seeds the built-in `zen` provider keyless and starts, so the GUI is reachable
  and keys can be added there — the documented path. Previously the process
  exited on first boot with an error naming a gitignored file that ships no
  example.

## [0.1.0] — 2026-09-27

First public release. Pre-1.0: the API and the SQLite schema may still change.

### Added

- Single static Go binary with the admin GUI embedded under the `prod` build
  tag. No cgo, no container runtime, no external services.
- Three client request surfaces — `/v1/chat/completions`, `/v1/responses`,
  `/v1/messages` — plus a merged `/v1/models` catalog and an open `/health`.
- Key pools per provider with `priority` and `lru` rotation, cooldowns that
  honor `Retry-After`, health tracking, and an optional per-key daily cap.
- Failure classification: genuine auth failures blacklist a key, rate limits
  and upstream flakes cool it down, client-shape rejections fail fast with an
  actionable hint.
- Self-describing model catalog: context window, max output, modalities, and
  reasoning flags, with an optional context-size suffix on model IDs.
- Anthropic-native forwarding for dual-endpoint providers, so thinking blocks
  with signatures, interleaved system messages, and agent fingerprints pass
  through untouched. Anthropic-only providers still serve OpenAI clients via
  request and response translation.
- Ten provider presets plus a generic adapter for any OpenAI- and/or
  Anthropic-compatible base URL, configurable from the GUI with no code.
- Built-in Claude Code integration: `claude-*` fallback rewriting, `[1m]`
  context suffixes, and a generated `settings.json`.
- Embedded admin GUI: users and roles, per-user client keys stored hashed,
  provider and upstream-key management, per-user provider revocation, a
  self-service profile, usage dashboards, and a live activity feed.
- Self-hosted MCP web tools at `/mcp` — `web_search` (SearXNG) and `web_read`
  (native fetch plus readability-to-markdown, with optional headless
  rendering) — with SSRF protection, per-user metering, and an idempotent
  installer.
- Runtime settings in the database, editable in the GUI and applied in-request
  with no restart.
- `deploy.sh` for a hardened systemd install, and a separate idempotent
  installer for the optional web-tool backends.
- Version stamping: `./nano-llm-proxy -version`.

### Security

- Captured agent fixtures are sanitized before commit, removing the
  operator's home directory paths and any personal instruction files a coding
  agent inlines into a request. `scripts/sanitize-fixtures.py` runs from the
  capture script and is asserted in CI.

### Known limitations

Documented in full under "Where this is deliberately not the best choice" in
the README. In short: three client protocols and no Gemini, Images,
Embeddings, or Rerank; no semantic cache or prompt compression; a single
instance with no shared pool across replicas; and upstream provider keys
stored reversibly in the SQLite file, so the database must stay at mode
`0600` on a host you trust.

[Unreleased]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/kmmuntasir/nano-llm-proxy/releases/tag/v0.1.0
