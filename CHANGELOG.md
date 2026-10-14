# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Release notes are generated from commit prefixes, so `feat:`, `fix:`,
`security:`, `ci:`, `docs:`, and `test:` all matter.

## [Unreleased]

### Added

- New upstream keys are verified before they are saved. Each key — added to
  a provider or among a new provider's initial keys — is probed against
  every endpoint the provider has (OpenAI and/or Anthropic): the model list
  plus a one-token call for a nonexistent model, so no inference runs and no
  quota is spent. An upstream 401/403 refuses the key with a 400 naming its
  label; an unreachable upstream refuses it with a 503. Pasted keys are
  trimmed of surrounding whitespace.

### Fixed

- The same upstream key can no longer be added to a provider twice (409),
  nor appear twice among a new provider's initial keys (400). Nothing
  enforced it before, despite the existing "already on this provider"
  message; a duplicate got double weight in key rotation.
- zen free models work again (2026-10-14). Zen's free-tier gate now
  requires opencode 2.0.29's inference path and real tool schemas: requests
  to `/zen/v1`, or whose `shell`/`read` tools carry empty parameter schemas,
  drew `403 FreeTierError` ("free tier can only be used from within
  OpenCode"). The built-in zen provider moves to
  `https://opencode.ai/inference/v1` (migration V10 updates it only if it
  still holds the old default), the injected gate tools carry opencode
  2.0.29's real parameter schemas, and the default zen user agent is
  `opencode/latest/2.0.29/cli`.
- Z.ai models work from Pi again: Pi sends the `developer` instruction role
  for reasoning models, which Z.ai rejects (`1214 Incorrect role
  information`). The OpenAI passthrough now renames it to `system` for every
  upstream except OpenAI, as the zen adapter already did.

### Changed

- Upstream failures are no longer answered with 502, which Cloudflare
  replaces with its own "Bad gateway" page (hiding the reason). A request
  the upstream refused as sent is a 400 (`upstream 4xx: …`); key exhaustion,
  outages and gate rejections are a 503 with the gateway's hint.

- zen free models work again. Since 2026-10-12 zen validates the
  `x-opencode-session` header as a real opencode identifier, and the
  gateway's random `ses_` ids drew `403 FreeTierError` on every request. The
  gateway now mints opencode-encoded session ids and sends the full opencode
  2.x fingerprint (`x-opencode-client`, `x-opencode-project`, the session id
  under all four session headers, `traceparent`/`b3`).
- Key cooldown logs show the upstream status (`HTTP 429`) instead of
  `HTTP 0`.
- Untagged builds (`go build` without `-tags prod`) no longer panic at
  startup on a duplicate `/` route; they serve the GUI-not-built stub.
- `/api/dashboard` no longer shows every user's recent requests to every
  logged-in user. Non-superadmins now get only their own activity entries
  (upstream key hash blanked) and pool health counts without per-key views.
- Client-facing errors for a rejected upstream key no longer include the
  key's label.
- Usage date ranges reject negative bounds and clamp `to` to now; extreme
  values could previously hang or exhaust memory in the bucketed endpoints.

### Changed

- Translated `/v1/messages` responses report usage in Anthropic semantics:
  `input_tokens` excludes cache hits, `cache_read_input_tokens` carries them
  and `cache_creation_input_tokens` is 0, so clients summing the three get
  the upstream's total prompt. `message_delta` now carries the full usage
  block (previously only `output_tokens`). Chat responses translated from
  Responses or Anthropic upstreams gain
  `prompt_tokens_details.cached_tokens` (and `total_tokens`).
- Default `zen.userAgent` is now `opencode/latest/2.0.27/cli`; the 1.18.0
  floor check accepts both the 1.x `opencode/X.Y.Z` and the 2.x
  `opencode/<channel>/X.Y.Z/<client>` shapes.
- The zen gate tool stubs are now `shell`/`read`/`edit`, matching the
  opencode 2.x tool set (1.x called the shell tool `bash`).

- zen models fronted by a strict role validator (e.g. `fledge-alpha-free`)
  no longer 502 for pi / oh-my-pi. Those clients send the instruction message
  as `role: "developer"` whenever the catalog says the model reasons — which
  our own `/v1/models` does — and the upstream answers
  `[airlock_error] invalid request: unknown variant 'developer', expected one
  of system, user, assistant, tool`. The zen adapter now renames the role to
  `system` on the way upstream (chat `messages` and Responses `input` alike):
  the older spelling of the same message, accepted everywhere, with content
  and message order untouched. opencode never sends the role, which is why
  the model worked there and failed here.

### Added

- Default providers for new users: the `newUsers.disabledProviderIds`
  runtime setting lists providers a newly created `user`-role account starts
  without (written as per-user revocations together with the account).
  Admins get every provider as before; existing users are not affected.
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
  per-user scoping; cross-user groupings are superadmin-only.
- Usage monitoring v2 (admin GUI). The Usage page becomes a tabbed section —
  Overview, Users, Models, Users × Models, Requests — with user and model
  drill-down pages, a shared filter bar (range presets, user, provider,
  model, client key, surface, status) kept in the URL, server-side paginated
  and sortable tables, and charts for requests/errors, tokens, latency and
  top-N series. Users and Users × Models are superadmin-only.
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
- Image input works on every path: the chat → Responses retyping now unwraps
  chat's nested `{"image_url":{"url":…}}` into the string the Responses API
  wants (muse-spark and friends used to reject the message with
  `input[0].content did not match any supported type`), and Anthropic image
  blocks on `/v1/messages` (how Claude Code sends pictures) become chat
  `image_url` parts instead of being dropped silently — the model used to
  answer about a picture it had never seen.
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

[Unreleased]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/kmmuntasir/nano-llm-proxy/releases/tag/v0.1.0
