---
name: create-implementation-plan
description: Read a ticket file (bug, feature, or enhancement), analyze the codebase, and write a comprehensive implementation plan. Use when the user hands you a ticket file path and wants an implementation plan generated.
---

# Create Implementation Plan Skill

Read the provided ticket carefully, understand what needs to be delivered, analyze the codebase, then write a complete and comprehensive implementation plan as a new markdown file in the **same folder** as the ticket.

The ticket may be a **bug**, **feature**, or **enhancement** — adapt the analysis focus and plan shape to the ticket type.

## Inputs

User provides a **ticket file path**, e.g.:

- `docs/bugfix/NL-300.md`
- `docs/tickets/NL-301-some-feature.md`
- Absolute or relative path to a single `*.md` ticket

(Path patterns are illustrative — always derive the real ticket path and ID from what the user hands you.)

If no input is provided, **ask** for the ticket file path. Do not guess.

## Execution Steps

Follow exactly, in order.

### Step 1: Read & understand the ticket

Resolve the input to an absolute path and read it **completely**. Extract and hold in context:

- **Ticket ID** (e.g., `NL-300`) — derive from the filename or the ticket heading
- **Ticket type** — bug / feature / enhancement. Infer from content: repro steps + expected/actual → **bug**; a new capability → **feature**; a modification/tweak to something existing → **enhancement**. State the assumption explicitly.
- **What needs to be delivered** — the requirement or defect, in your own words
- **Named endpoints, tables, providers, roles, surfaces** (gateway / GUI)
- For bugs: the **steps to reproduce** + expected vs. actual result

State your understanding back before analyzing: "Read ticket NL-300 (bug) — <one-line summary>. Analyzing codebase..." (swap the type and summary as appropriate).

### Step 2: Analyze the codebase

Use up to **3 parallel `analyst` subagents** (via the Agent tool, `subagent_type: analyst`) to investigate and keep the main context window clean. **The split adapts to the ticket type.**

**For a bug** — focus on the defect:

| Subagent | Responsibility |
|----------|---------------|
| **Repro path** | Trace the reproduction path end-to-end. Locate the handler/adapter/store function named in the ticket, read the exact code path, and confirm where the buggy behavior occurs. Cite `path:line`. |
| **Root cause** | Pinpoint the defect — the missing guard / wrong branch / bad assumption, *why* it allows the bad behavior, and where the correct check belongs (handler in `internal/gateway/` → store in `internal/store/`; adapters/pool logic stay out of handlers). |
| **Prior art & fix surface** | Map patterns to reuse: similar existing guards, the `apiErr`/`writeJSON` error shape, the closest existing `*_test.go` and mock-upstream fixtures, and any GUI impact. |

**For a feature / enhancement** — focus on the design surface:

| Subagent | Responsibility |
|----------|---------------|
| **Integration points** | Where the capability plugs in: route registration (`internal/gateway/routes.go`), the handler file (`api_*.go` for the admin API, adapter files for `/v1/*`), `internal/store/store.go` (new in-code `migrateVn()` if the schema changes), `internal/settings/` for runtime knobs, `internal/config/` for bootstrap env, and any new API contract. Cite `path:line`. |
| **Patterns & conventions** | Existing precedents to mirror: analogous handlers (`readJSON`/`pathID`/`apiErr` helpers), pool/adapter patterns, error classification, naming, `.env.example` + `docs/configuration.md` for any new env var. |
| **Cross-cutting & GUI** | Auth/session/Origin checks, client-key hashing, upstream-key redaction, SSRF guard for anything fetching URLs, dependency impact (a new Go module needs justification — 5 direct deps today), memory impact (512 MB VPS target), and GUI impact (`web/src/api/client.ts` + `types.ts`, page in `web/src/pages/`, components in `web/src/components/`, TanStack Query keys). |

Backend is Go + SQLite: `main.go` + `internal/{config,gateway,settings,store,webtools}`, tests as `*_test.go` beside the code. The GUI is `web/` (React 19 + TypeScript strict + Vite + Chakra UI v3 + TanStack Query + react-router-dom; no test framework). Docs live in `docs/` (`api.md`, `configuration.md`, `deployment.md`).

Each subagent returns a **curated digest** with `path:line` evidence — not raw file dumps. Work from those digests.

If the ticket is clearly single-layer or small, drop to 1–2 subagents. Add more `analyst` calls only if a digest surfaces a new area worth a focused probe.

### Step 3: Synthesize the approach

Combine the digests into a single coherent picture:

- **Bug** → state the root cause (what + why) and the minimal, convention-correct fix set
- **Feature / enhancement** → state the design: new/changed table + `migrateVn()`, store functions, handler/adapter, route, API contract, GUI pieces — and a sensible build order (migration → store → handler/adapter → route → tests → GUI → docs)
- **Both** → list edge cases & risks (concurrency, scheduler double-processing, related paths needing the same change, regressions, migration concerns) and any open questions

Respect project conventions: handlers do HTTP only and delegate to store/pool/adapter code; admin handlers answer errors via `apiErr` (`{"error":{"message":...}}`); schema changes are a new in-code `migrateVn()` recorded in `schema_migrations` (never edit an applied one); runtime-tunable values go in `internal/settings`, bootstrap values in `internal/config`; formatting is `gofmt`.

Project invariants every plan must preserve:
- Single static binary: pure Go, no cgo, no Docker, no sidecars; a new direct dependency needs explicit justification (5 today).
- Memory stays small (512 MB VPS target) — no unbounded buffers or caches.
- SQLite (WAL) is the source of truth; GUI mutations rebuild in-memory pools in the same request; single instance only.
- Secrets: client keys (`fg-…`) stored hashed; upstream keys never logged or returned after creation; `.env`, `keys.json`, `gateway.db` never committed.
- Auth: session cookie + Origin check on `/api` mutations; bearer/`x-api-key` on `/v1/*` and `/mcp`; per-user provider access respected in routing and `/v1/models`.
- Anything that fetches a URL on a user's behalf goes through the SSRF guard (`internal/webtools/ssrf.go`).
- Tests run offline against scripted mock upstreams; fixtures in `internal/gateway/testdata/` are reference material, sanitized, never read by tests.
- Docs stay honest: update README "deliberately not the best choice" section only if a gap actually closes; update `docs/` and `CHANGELOG.md` for user-visible changes.
- Destructive actions in the GUI use a confirmation dialog (`ConfirmDialog.tsx`).

### Step 4: Write the implementation plan

Write the plan to the **same directory as the ticket**, named `{ticket-filename}-plan.md` — e.g. ticket `docs/bugfix/NL-300.md` → `docs/bugfix/NL-300-plan.md`. Use the template below; include the **Root Cause** section **only for bugs**.

## Plan Template

```markdown
# Implementation Plan — {TICKET_ID}

**Ticket:** `{path-to-ticket}`
**Type:** {Bug | Feature | Enhancement}
**Title:** {ticket title}
**Generated:** {ISO date}

---

## Summary

{1–2 paragraph restatement of what needs to be delivered, in your own words.}

## Root Cause  *(bugs only — omit for feature/enhancement)*

{The precise defect: what is wrong and why it happens, with `path:line` evidence.}

## Affected Components

| Layer | File | Why |
|-------|------|-----|
| Handler | `internal/gateway/api_xxx.go` or adapter file | ... |
| Routes | `internal/gateway/routes.go` | ... |
| Store / migration | `internal/store/store.go` (`migrateVn()`) | ... |
| Settings / config | `internal/settings/settings.go`, `internal/config/config.go` (+ `.env.example`, `docs/configuration.md`) | ... |
| Tests | `internal/<pkg>/xxx_test.go` | ... |
| GUI client | `web/src/api/client.ts`, `web/src/api/types.ts` | ... |
| GUI page/component | `web/src/pages/XxxPage.tsx`, `web/src/components/Xxx.tsx` | ... |
| Docs | `docs/api.md`, `README.md`, `CHANGELOG.md` | ... |

## Proposed Implementation

{Step-by-step. One sub-section per change, each with **File** / **What** / **Why** / **Code reference** (existing function/line the change builds on). Group gateway and GUI separately. For features/enhancements, order changes by build dependency.}

### Gateway Changes
...

### GUI Changes
*(only if the ticket or fix touches `web/`)*
...

## Edge Cases & Risks

- {concurrency / scheduler idempotency / enum + migration interaction / employee-vs-admin access matrix / related paths / regressions / migration concerns}

## Testing

*Follow project conventions — standard `testing` package, `*_test.go` beside the code, scripted mock upstreams and local fixture servers, no network, no API keys. The GUI has NO test framework — its verification is `npm run build` (runs `tsc -b`).*

- **Unit tests:** {handler/pool/store cases — happy + error paths}
- **GUI verification:** `cd web && npm run build` (no test stack exists — do not invent one)
- **Manual verification:** {re-run the ticket's reproduce steps for bugs / exercise the new capability for features}
- **Gate:** `go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"`; GUI build green if `web/` changed. Single-test reruns use `go test -race ./internal/gateway -run TestName`.

## Acceptance Criteria

- [ ] {verifiable outcome — mirrors the ticket's "Expected Result" / acceptance criteria}
- [ ] ...

## Open Questions  *(optional)*

- {anything needing a product/owner decision}

## Out of Scope

- {anything explicitly not addressed}
```

## Error Handling

- **Can't read ticket** — ask the user to verify the path; do not proceed.
- **Ticket has no ID** — derive a slug from the filename; flag it in the plan.
- **Ticket type unclear** — state your best inference and why; proceed on that basis and note it.
- **Approach ambiguous** (e.g. unclear root cause, or a feature with multiple valid designs) — document the leading approach with evidence, list the alternatives, and mark what needs confirmation. Do not fabricate `path:line` citations.
- **Subagent failure** — retry the failed `analyst` individually; note in the plan if an area could not be fully investigated.

## Key Principles

- **Delegate analysis, write yourself.** Keep the main context clean — investigate via `analyst` subagents, synthesize and write the plan directly.
- **Evidence-backed.** Every code claim cites `path:line`. No guesses presented as fact.
- **Convention-correct.** Respect the project's layering (handler → pool/adapter/store) and its style/error/testing conventions; never propose bypassing the SSRF guard, session/Origin checks, or key redaction.
- **Adapt to the ticket type.** Bugs hunt a root cause; features/enhancements lay out a design. Same plan skeleton, type-appropriate emphasis.
- **Comprehensive but minimal.** Cover the full surface (including related paths needing the same change) without scope creep. Out-of-scope items are called out explicitly.
