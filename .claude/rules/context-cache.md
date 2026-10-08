# .context Structure + Findings Cache

`.context/` is committed working state for the agentic pipeline. Structure:

```
.context/
  dev-cycle/
    state.md                  # dev-cycle phase machine
  pm-cycles/
    pm-cycle-<ts>/            # unchanged — PM cycle convention (see product-management skill)
      state.md, questions/, deliverables/, deliverables.md
  tickets/
    NL-<n>/                   # per-ticket working state
      ticket.md               # ticket body (derived from a deliverable)
      findings/
        codebase-<sha7>.md    # analyst codebase digests (scoped)
        impl-delta.md         # appended by coder agents: what changed + why
        verify-<date>.md      # per-AC verdict table from verify-deliverable
      report.md               # final: commits, test results, verdict
  cache/
    codebase-map.md           # living architecture map, sections per subsystem
    decisions.md              # append-only decision log
```

## Findings file contract

Every findings file starts with a header:

```markdown
---
based-on: <commit-sha>       # HEAD sha when the analysis was made
date: YYYY-MM-DD
scope:                       # files/globs the analysis covers
  - internal/gateway/**
  - web/src/api/**
---
```

## Freshness check (before trusting findings)

One command:

```bash
git diff --name-only <based-on>..HEAD -- <scope>
```

- Empty → fresh. Use as-is. Do NOT re-analyze.
- Non-empty → stale only for the changed paths. Reuse untouched sections; refresh only changed paths.

## Update rules (consumer-driven, lazy — no dedicated refresh step)

1. **Partial refresh** — stale sections re-analyzed (analyst subagent or direct read), untouched sections preserved, header rewritten with new sha + date.
2. **Gap read** — stale but diff is small (rename, single function): read the file(s) directly, skip analyst spawn entirely.
3. **Full refresh** — scope invalid (large refactor): discard, re-map, new file.
4. **Write-back** — anyone who consumed findings AND explored beyond them updates the file before finishing. Updates ride along work already in progress.
5. **Impl deltas** — coder agents append a short delta to the ticket's `findings/impl-delta.md` after implementing (files changed, behaviors added, decisions made). Later stages read the delta instead of re-deriving.

## `cache/codebase-map.md`

Living file; one section per subsystem (gateway routes, store/schema, provider adapters, web pages, etc.), each section carries its own `based-on: <sha>`. Same freshness/update rules per section. The **verify stage is the designated map-maintainer**: it diffs everything anyway, so after verifying it rewrites sections covering changed files.

## `cache/decisions.md`

Append-only: one line per decision — `YYYY-MM-DD NL-<n>: <decision> (context)`. Plans read it to avoid relitigating settled choices; PM cycles read it to avoid re-asking settled questions.

## Decide-then-read

Every stage that needs codebase understanding: read relevant findings FIRST, run the freshness check, and only explore what the findings don't cover. Never re-derive what fresh findings already state.
