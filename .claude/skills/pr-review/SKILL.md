---
name: pr-review
description: Comprehensive PR review covering the Go + SQLite gateway and the React 19 / TypeScript / Chakra UI admin GUI of nano-llm-proxy: correctness, security, dependency discipline, and test coverage. Use when user requests to review a pull request or compare branches for code review.
---

# PR Review Skill

When user requests **PR review** or to **compare branches**:

### Branch Defaults

- **Source branch**: Current local branch. Determine with `git branch --show-current`.
- **Target branch**: `main`, unless user explicitly specifies different branch.
- If user specifies both branches, use those values.

### Pre-Review: Branch Synchronisation

Before review, both branches must be up-to-date and source must be rebased onto target.

**Standard mode** (online):

```bash
# 1. Fetch all remotes
git fetch --all

# 2. Reset target to origin
git checkout <target-branch> && git reset --hard origin/<target-branch>

# 3. Reset source to origin
git checkout <source-branch> && git reset --hard origin/<source-branch>

# 4. Rebase source onto target
git rebase <target-branch>
```

**Offline mode**: If user says **"offline"** when invoking this skill, skip steps 1-3 entirely. Only run rebase (step 4) against local copy of target branch. Allows reviewing purely local state without network access.

**Conflict handling**: If rebase in step 4 produces merge conflicts, **stop entire review**. Abort rebase (`git rebase --abort`), inform user of conflicts, do not proceed with any review steps.

**If rebase succeeds**: Proceed to review steps below.

### Parallel Subagent Strategy

Review accelerates using **up to 3 parallel subagents** (via `Agent` tool). Split independent review tasks across subagents to save context window and speed process. Example parallelisation:

| Subagent | Scope | Agent Type |
|----------|-------|------------|
| 1 | Diff analysis + architecture review | `general-purpose` |
| 2 | Stack-specific checks (Go gateway + React/Chakra GUI) | `general-purpose` |
| 3 | Test coverage assessment + code quality checklist | `general-purpose` |

**When to parallelise:** Always use parallel subagents when diff is non-trivial (more than few files). For tiny diffs (1-2 files, cosmetic changes), single-pass review fine.

**How to parallelise:** Launch all independent subagents in single message using multiple `Agent` tool calls. Each subagent receives diff (via `git diff`) and its specific review scope. After all subagents return, synthesize findings into final review summary (step 6).

## 1. Run Complete Diff

Compare source branch against target branch. Analyze **actual code changes**, not just commit messages.

```bash
git diff target..source
git log target..source --oneline
```

## 2. Identify Change Types

Determine what each change represents:
- Feature addition
- Bug fix
- Refactor
- Cleanup
- Potential breaking change

Note: missing tests, incomplete docs, inconsistencies.

## 3. Assess Code Quality & Impact

Evaluate:
- **Correctness**: Does code work as intended?
- **Readability**: Is code understandable?
- **Maintainability**: Will this be easy to modify later?
- **Architectural Alignment**: Does it follow project's patterns?
- **Performance Implications**: Any performance concerns?
- **Security Considerations**: Any vulnerabilities?

Check whether tests adequately cover changes.

## 4. Stack-Specific Review Items

### 4a. Go gateway (`main.go`, `internal/*`)

**Correctness & concurrency**
- Pool state (cooldowns, blacklist, LRU/priority pick) mutated only under its lock; `go test -race` clean?
- Failure classification intact: genuine 401 blacklists, 429/5xx cool down, client-shape errors fail fast, failover stops at first streamed byte?
- Streaming paths flush correctly, close upstream bodies, honor context cancellation; no goroutine or response-body leaks?
- Errors handled or logged — no ignored `err` on paths that matter; no `panic` in request paths?
- Protocol translation (OpenAI chat / Responses / Anthropic) preserves fields clients depend on (usage, tool calls, reasoning, images)?

**Persistence**
- Schema changes are a new in-code `migrateVn()` recorded in `schema_migrations` — applied migrations never edited?
- SQL uses `?` placeholders — no string-built queries from user input?
- GUI mutations still rebuild the in-memory pools in the same request?

**Security**
- Client keys (`fg-…`) stay hashed at rest and plaintext shown once; upstream keys never logged, returned after creation, or put in error messages?
- `/api/*` mutations keep session-cookie auth + Origin check; role checks (superadmin vs user) enforced server-side, not just hidden in the GUI?
- `/v1/*` and `/mcp` auth unchanged; per-user provider access respected in routing and `/v1/models`?
- Anything fetching a URL on a user's behalf goes through the SSRF guard (`internal/webtools/ssrf.go`); redirects and DNS rebinding considered?
- No secrets, tokens, or full request/response bodies logged; `.env`, `keys.json`, `gateway.db` untouched and uncommitted?
- Login throttling, cookie flags (`Secure`, `HttpOnly`, `SameSite`) not weakened; default bind stays loopback?

**Dependencies & footprint**
- No new direct Go module without justification in the PR description (5 today, all pure Go); no cgo; no Docker/sidecar introduced?
- Memory-conscious (512 MB VPS target): no unbounded buffers, caches, or per-request full-body reads without limits?

**Config & docs**
- New env vars appear in `.env.example` and `docs/configuration.md`; runtime-tunable values live in `internal/settings` (DB) rather than env?
- User-visible changes reflected in `docs/api.md` / `README.md` / `CHANGELOG.md`; README's "deliberately not the best choice" section stays honest?
- Fixtures in `internal/gateway/testdata/` stay sanitized (`scripts/sanitize-fixtures.py --check`)?

### 4b. Admin GUI (`web/`: React 19 / TypeScript / Chakra UI v3 / TanStack Query)

**Data & API**
- All requests via the helpers in `web/src/api/client.ts` (`api/post/put/patch/del`) — no raw `fetch` scattered in components, no new HTTP library?
- Server data via TanStack Query with stable `queryKey`s; mutations invalidate the affected keys?
- Response/request types in `web/src/api/types.ts` mirror the gateway's JSON exactly — no invented fields?
- 401 handling stays centralized (`setUnauthorizedHandler`); no token stored in `localStorage` (auth is cookie-based)?
- Error messages from `ApiError` surfaced via the toaster — no swallowed rejections?

**TypeScript**
- `import type` for type-only imports (`verbatimModuleSyntax`); no unused locals/params (`tsc -b` fails the build); no `any` where a type exists?

**Components & UX**
- Chakra UI v3 components and the existing `components/ui/` wrappers reused; no second UI library or CSS framework introduced?
- Shared pieces (`DataTable`, `ConfirmDialog`, `CopyButton`, `KeyRevealDialog`, `StatusBadge`) reused before new markup?
- Destructive actions gated behind `ConfirmDialog`; secrets shown once via `KeyRevealDialog`?
- Admin-only pages stay behind the role check in `App.tsx` (server enforces too)?
- Layout holds at mobile width?

## 5. Test Coverage

- **Go tests** present for new logic: standard `testing`, `*_test.go` beside the code, scripted mock upstreams / local fixture servers, table-driven where natural; **offline** — no network, no API keys, no live providers?
- Edge cases covered alongside happy paths: failover and cooldown math, blacklist vs cooldown classification, streaming vs non-streaming, per-user provider revocation, auth failures (401/403), SSRF-blocked targets, bad JSON (400)?
- **GUI:** no test framework exists — confirm the author did NOT scaffold ad-hoc test deps, and that `cd web && npm run build` passes instead?
- **Gate (authoritative pre-review signal):** `go vet ./...`, `go test -race ./...`, `gofmt -l .` prints nothing, and `cd web && npm run build` green. Single-test reruns use `go test -race ./internal/gateway -run TestName`.

## 6. Provide Senior-Level Review Summary

Offer direct, actionable feedback:
- Call out risks
- Highlight strengths
- Suggest improvements
- Indicate whether changes ready to merge or need revisions (backed by green test/build runs)

## 7. Aim for Practical, High-Value Feedback

Goal: emulate real PR review from experienced engineer — clear, specific, focused on what matters.

## 8. Write Comprehensive PR Review Report

Write comprehensive PR review report as markdown file, save in `docs/ai-generated/` directory (gitignored scratch). Report includes:
- Summary of changes
- Code quality assessment
- Performance considerations
- Security implications
- Testing coverage
- Recommendations
- Whether changes ready to merge or need revisions

---

## Go / React Code Review Checklist

### Architecture & Design
- [ ] Follows the existing layout (`internal/{config,gateway,settings,store,webtools}`; `web/src/{api,components,pages}`)
- [ ] Handlers stay thin; pool/adapter/store logic lives in its package, not inlined in handlers
- [ ] Runtime-tunable values in `internal/settings`; bootstrap values in `internal/config` + `.env.example`
- [ ] One concern per PR

### Go
- [ ] `gofmt` clean; `go vet` clean
- [ ] Errors checked and wrapped/logged; no swallowed failures
- [ ] Locks held for state mutation only; no lock held across network I/O
- [ ] Context propagated and honored; response bodies closed
- [ ] Admin errors via `apiErr` (`{"error":{"message":...}}`); no internals leaked

### Persistence
- [ ] Schema change = new `migrateVn()`; parameterized SQL only
- [ ] Pools rebuilt after GUI mutations

### Security
- [ ] Client keys hashed; upstream keys never logged or re-exposed
- [ ] Session + Origin checks intact on `/api/*` mutations; server-side role checks
- [ ] SSRF guard on every user-driven fetch
- [ ] No secrets or runtime state files committed; loopback bind default preserved

### Dependencies
- [ ] No new direct module (or justified); no cgo; no Docker

### React (GUI)
- [ ] Functional components + hooks; API access through `web/src/api/client.ts`; TanStack Query keys stable
- [ ] Types mirror gateway JSON; `import type` used; no unused locals
- [ ] Chakra UI v3 only; confirmation dialog for destructive actions
- [ ] UI copy and mobile layout reasonable

### Performance
- [ ] Bounded memory (512 MB target); streaming not buffered fully
- [ ] No per-request work that scales with total usage history; queries indexed for new filters

### Testing
- [ ] Offline Go tests for new logic, including error paths
- [ ] Go gate and `cd web && npm run build` pass

### Code Quality
- [ ] Naming and comment density match the surrounding code; comments explain *why*
- [ ] Commit messages follow `<type>: <subject>` (feat/fix/docs/test/ci/security/release/meta/refactor/chore)
