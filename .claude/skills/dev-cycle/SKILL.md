---
name: dev-cycle
description: Full agentic development cycle — product management (clarification rounds) → NL tickets → batch approval → per-ticket build (plan, tasks, implement, commit) → verify against deliverable → done or fix loop. State-driven and resumable — re-run /dev-cycle (no args) to advance from wherever the cycle paused. Use when user wants the whole flow from issues to committed implementation.
---

# Dev Cycle Skill

State-machine sequencer chaining existing skills:

```
/product-management → /convert-to-tickets → [user approves batch] → /handle-ticket (per ticket) → /verify-deliverable (per ticket) → done | fix loop
```

You are the thin sequencer. State lives in `.context/dev-cycle/state.md`. Every invocation: read state → run the current phase → update state → stop at the next pause point. You never do phase work yourself — invoke the sub-skill or tell the user what's needed.

## State file

```markdown
# dev-cycle state
phase: pm | tickets | awaiting-approval | build | verify | fix | done
started: YYYY-MM-DD HH:mm
pm_cycle: .context/pm-cycles/pm-cycle-<ts>     # set at phase tickets
tickets:                                       # set at awaiting-approval
  - NL-101
  - NL-102
approved: [NL-101, NL-102]                 # set when user approves
ticket_index: 0                                # build/verify cursor
current_ticket: NL-101
fix_rounds: {}
log:
  - <one line per phase transition, timestamped>
```

## Invocation modes

- **With issues** (text or file path) → new cycle: write fresh `state.md` (`phase: pm`), then run PM phase.
- **No args** → resume: read `state.md`, run current phase. If no state file exists, ask for issues.
- **`--new`** with issues → archive old state (rename `state.md` → `state-<date>.md`), start fresh.

## Authorization

Invoking this skill **is standing approval** for everything its sub-skills authorize (PM cycle folders; ticket file writes; branch sync from origin/main + ticket branch creation; per-task commits via `committer`; Go and GUI build/test runs), **plus pushing the ticket branch** to origin after each ticket's build+verify completes (`git push -u origin <ticket-branch>` — branch only, never from `main`).

**Never** merge, rebase, amend, or force-push, and never push `main` — it remains the user's call. PRs via `gh` are also the user's call unless they ask otherwise.

## Phases

### pm — invoke `product-management`

`Skill("product-management", args: "<issues or empty>")`.

- PM returns **awaiting answers** → tell the user which question file to answer and that re-running `/dev-cycle` continues. Stop.
- PM returns **deliverables complete** → capture `pm_cycle` folder, advance to `tickets`.

### tickets — invoke `convert-to-tickets`

`Skill("convert-to-tickets", args: "<pm_cycle>/deliverables.md")`.

Capture generated ticket ids. Advance to `awaiting-approval`.

### awaiting-approval — human gate

Show the user: ticket table (id, title, source DEL, dependency order) + suggested build order (dependencies first). Ask: ship all / subset (by id) / hold. Record `approved`, order it, reset `ticket_index: 0`, advance to `build`.

Use AskUserQuestion here — this is a one-shot interactive gate, not PM-style file clarification.

### build — invoke `handle-ticket` per approved ticket, sequentially

`Skill("handle-ticket", args: "docs/tickets/<ticket-file>.md")` for `approved[ticket_index]`.

One at a time — each ticket creates its own branch from origin/main, so parallel is impossible in one working tree.

- Ticket completes → capture its report (commits, branch) into `state.md` log, increment `ticket_index`.
- **Push happens after verify accepts** (see `verify` phase), not here — an unverified ticket is never pushed. Exception: if `verify` verdict is `gaps-found` and the fix loop exhausts, ask the user whether to push anyway or leave local.
- `handle-ticket` blocker → surface, stop, phase stays `build`.
- All tickets built → advance to `verify`.

Note: `handle-ticket` phase 4 runs `verify-deliverable` internally. The dedicated `verify` phase below is the authoritative deliverable-level gate; if handle-ticket's run already produced an accepted verdict for a ticket, you may reuse it and skip re-verification.

### verify — invoke `verify-deliverable` per ticket, sequentially

`Skill("verify-deliverable", args: "<ticket-file> + <plan-file> + base main")`.

- Verdict **accepted** → **push the ticket branch**: `git push -u origin <current-ticket-branch>` (authorized above). Record the remote branch in `state.md`, mark ticket done, next ticket.
- Verdict **gaps-found** → advance phase to `fix` with `current_ticket` set.
- All verified → phase `done`.

### fix — targeted gap fixes (max 2 rounds per ticket)

From the verify report's "Gaps requiring action":

1. Write a minimal fix-tasks file `.context/tickets/<id>/fix-round-<n>-tasks.md` (one task per gap, acceptance criterion quoted).
2. Invoke `Skill("orchestrator", args: "<fix-tasks-file>")` — coders fix, `committer` commits per fix task.
3. Re-invoke `verify-deliverable`.
4. Accepted → back to `verify` (resume remaining tickets). Still gaps after round 2 → **stop**, report gaps, phase stays `fix` — user decides (manual fix, more rounds, or accept partial). Do not loop forever.

### done — final report

Concise summary:

- Cycle folder + tickets: id, title, branch (local + pushed remote URL), commits (hash + subject)
- Per-ticket verdict: accepted / partial (what's open), test results
- Unverifiable ACs requiring manual checks (e.g. a live upstream provider call)
- Next steps: review pushed branches + open PRs + merge = user's call; prune `.context/tickets/<id>/` folders or copy `report.md` to `docs/ai-generated/` if desired

## Pause points (where you stop and why)

| Phase | Pause | Resume |
|-------|-------|--------|
| pm | questions await answers | user answers, re-runs `/dev-cycle` |
| awaiting-approval | batch approval | user picks tickets |
| fix | round-2 gaps persist | user decides |
| done | cycle complete | `--new` for next cycle |

## Error handling

- Sub-skill artifact missing after it returns → re-check expected path; if truly absent, report and stop (later phases depend on it).
- State file corrupt/ambiguous → show it to the user, propose a repair, do not guess silently.
- Skill tool fails to load a sub-skill → report, stop.
- Never advance phase on a failed sub-step.

## Key principles

- **Thin sequencer.** Phases belong to sub-skills; you read state, invoke, capture, relay.
- **Resumable by construction.** All progress in `state.md`; any crash/session-end resumes cleanly.
- **Cache-aware.** Sub-skills handle `.context` findings per `.claude/rules/context-cache.md`; you never pull source into main context.
- **Two human gates only**: PM answers + batch approval. Fix-round-2 exhaustion adds a third only when something is wrong.
