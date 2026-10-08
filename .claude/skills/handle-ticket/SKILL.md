---
name: handle-ticket
description: End-to-end ticket handler. From a ticket file, sync from origin/main, create the ticket branch, create an implementation plan, break it into tasks, orchestrate implementation with per-task commits, then verify and report. Use when the user wants one ticket handled start-to-finish.
---

# Handle Ticket Skill

Run a single ticket end-to-end by chaining four existing skills in order:

```
ticket → /create-implementation-plan → /breakdown-plan-into-tasks → /orchestrator → /verify-deliverable
```

Each sub-skill is invoked **sequentially** via the **Skill tool** (one at a time — never nested/parallel). You drive the chain in the main context: invoke a sub-skill, let it finish and write its artifact, capture the artifact path, pass it to the next step.

## Authorization (read before running)

Invoking this skill **is standing approval** to:

- **Sync the working tree from `origin/main` and branch for the ticket** — `git fetch --all`, then `git checkout main`, `git reset --hard origin/main` (**discards any uncommitted local changes — intentional**), then create + checkout the ticket branch. Skip the sync/branch only if you are already on the ticket branch.
- **Commit** via the `committer` agent — **one commit per task**, project convention (`<type>: <subject>`, e.g. `fix: stop cooldown reset on probe success`); no per-commit confirmation pause. Ticket id goes in the body, not the subject.
- **Run the Go gate** (`go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"`) for verification **by default**. (Fall back to static review only if the user has stated they have no local Go toolchain — see Phase 4.)
- **Run the GUI build** (`cd web && npm run build`, which runs `tsc -b`) when `web/` is touched — there is no GUI test framework.
- **Never** push, merge, rebase, amend, or force-push — those remain the user's call.

State this to the user before starting: "Handling {ticket}. This will sync from origin/main, branch, implement, commit per task, and verify. Local uncommitted changes will be discarded. Push/merge stays your call."

## Inputs

User provides a **ticket file path**, e.g.:

- `docs/tickets/NL-300-fix-cooldown.md`
- `docs/tickets/NL-301-some-feature.md`
- Absolute or relative path to a single `*.md` ticket

If no input is provided, **ask** for it. Do not guess.

## Setup

1. Resolve the ticket path to absolute. Read it to capture:
   - **Ticket ID** (e.g. `NL-300`) — from the heading or filename
   - **Type** — bug / feature / enhancement (infer from content)
   - **Slug** — short, hyphenated, lowercased, derived from the title
2. Compute the branch name (project convention `feat/<slug>` / `fix/<slug>`, see `.claude/rules/git-guidelines.md`):
   - bug → `fix/<slug>`
   - feature / enhancement → `feat/<slug>`
3. **Branch setup** (authorized above). Determine the current branch:
   - **Already on the ticket branch** → no action.
   - **Otherwise** → run, in order:
     ```
     git fetch --all
     git checkout main
     git reset --hard origin/main
     git checkout -b <ticket-branch>
     ```
4. Track the phases below with the task tools (`TaskCreate` → `in_progress` → `completed`) for visibility.

Artifact paths chain by naming convention (example for `docs/tickets/NL-300.md`):

| Artifact | Path |
|----------|------|
| Plan | `docs/tickets/NL-300-plan.md` |
| Tasks | `docs/tickets/NL-300-plan-tasks.md` |
| Verification | `.context/tickets/NL-300/findings/verify-<YYYY-MM-DD>.md` — written by `verify-deliverable`; summary line appended to `.context/tickets/NL-300/report.md` |

## Phases

### Phase 1 — Plan

Invoke the **`create-implementation-plan`** skill via the Skill tool: `skill: "create-implementation-plan"`, `args: "<ticket-path>"`.

It reads the ticket, analyzes the codebase, and writes the plan to the **same folder** as the ticket.

After it finishes: verify the plan file exists (expected `{ticket-dir}/{ticket-basename}-plan.md`), then capture its absolute path as **`planFile`**. If the sub-skill reported a different path, use that.

### Phase 2 — Break into tasks

Invoke the **`breakdown-plan-into-tasks`** skill: `skill: "breakdown-plan-into-tasks"`, `args: "<planFile>"`.

It analyzes the plan against the codebase and writes a parallelizable task breakdown alongside it.

After it finishes: verify the tasks file exists (expected `{plan-dir}/{plan-basename}-tasks.md`), capture its absolute path as **`tasksFile`**.

### Phase 3 — Implement (orchestrate + per-task commits)

Invoke the **`orchestrator`** skill: `skill: "orchestrator"`, `args: "<tasksFile>"`.

- Let the orchestrator implement and verify **all** tasks (it dispatches to `go-coder` / `react-coder` as the task layers dictate) and commit **per task** via the `committer` agent — its default. Pass the **ticket ID** as context so each commit body references it (`Ref: NL-<n>`).
- Do **not** override the per-task cadence. Never push.

Capture the list of files that changed and the commit hashes.

If the orchestrator reports a blocker or a coder returns a failure, surface it and stop (do not silently skip or commit a broken task).

### Phase 4 — Verify (build + completeness)

Two complementary checks:

1. **Completeness** — invoke the **`verify-deliverable`** skill: `skill: "verify-deliverable"`, `args: "<ticket-path> + <planFile> + base main"`. It checks acceptance criteria against the implementation (intent conformance) AND the plan against the implementation, then writes a per-AC verdict report to `.context/tickets/<ticket-id>/findings/verify-<YYYY-MM-DD>.md` (its canonical location per `.claude/rules/context-cache.md`). Capture the path it reports as **`verificationFile`**.
2. **Build/tests** — **run the Go gate by default** (it must pass green before reporting done):
   ```
   go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"
   ```
   If `web/` is touched too, also run:
   ```
   cd web && npm run build   # no test framework exists — the tsc -b + vite build is the gate
   ```
   Fold the result (pass/fail, failing tests if any) into the final report.

   **Static review fallback** — only if the user has stated they have no local Go toolchain, **skip** the test run and rely on the `verify-deliverable` report alone. In that case, state plainly that tests were **not** executed and verification is **static review only**.

### Phase 5 — Docs commit

Commit the planning artifacts produced by Phases 1, 2, and 4 as a single docs commit via the `committer` agent: the plan and tasks files (beside the ticket in `docs/tickets/`) plus the verification report (under `.context/tickets/<ticket-id>/findings/`), e.g. `docs: add implementation plan, tasks, verification for NL-300`. Stage explicit paths from both locations. (Skip if there are none or the user declines.)

## Output (final report)

Return a concise end-to-end summary:

- **Ticket** — ID, type, title, one-line summary; branch created/used
- **Artifacts** — links to `planFile`, `tasksFile`, `verificationFile`
- **Implementation** — what got built (per task area, files touched)
- **Commits** — hash + message for each per-task code commit, plus the docs commit
- **Verification outcome** — Go gate result, GUI build result (or explicit "not run — static review only"); completeness counts (✅ implemented / ⚠️ partial / ❌ missing / 🔄 modified); any gaps surfaced
- **Open items** — anything blocked, failing tests, or left to the user (push/merge, manual runtime testing, etc.)

## Error Handling

- **Ticket unreadable / missing ID** — ask the user; do not proceed.
- **Sub-skill's artifact not found after it returns** — note the expected path, re-check, and ask the user before continuing (the next phase depends on it).
- **Orchestrator blocker / coder failure** — do not commit further; report and stop.
- **The Go gate / `npm run build` fails or is unavailable (and not pre-declared)** — report the failure verbatim; fall back to static review for the report; do not claim green tests.
- **Verification finds gaps** — do not auto-fix; surface them in the final report for the user to decide.

## Key Principles

- **Sequential sub-skills.** One Skill-tool invocation at a time; capture each artifact before invoking the next.
- **Per-task commits, project convention.** Let the orchestrator commit after each task; never push.
- **Branch fresh from origin/main.** Fetch, reset hard, branch — unless already on the ticket branch.
- **Verify for real by default.** Run the Go gate and `cd web && npm run build` (when `web/` is touched); only fall back to static review when the user has no local toolchain, and say so explicitly.
- **Honest reporting.** Never claim a build/test passed that wasn't actually run.
