# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Release notes are generated from commit prefixes, so `feat:`, `fix:`,
`security:`, `ci:`, `docs:`, and `test:` all matter.

## [Unreleased]

Nothing yet.

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

[Unreleased]: https://github.com/kmmuntasir/nano-llm-proxy/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/kmmuntasir/nano-llm-proxy/releases/tag/v0.1.0
