# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Release notes are generated from commit prefixes, so `feat:`, `fix:`,
`security:`, `ci:`, `docs:`, and `test:` all matter.

## [Unreleased]

### Fixed

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
