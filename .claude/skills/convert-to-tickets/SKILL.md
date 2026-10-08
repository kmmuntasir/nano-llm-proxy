---
name: convert-to-tickets
description: Convert PM deliverables into NL-numbered ticket files under docs/tickets/. Reads a deliverables index (or deliverable files), assigns sequential NL ids, writes one ticket file per deliverable with the requirements body carried over verbatim. Use after a product-management cycle completes, or when user asks to turn deliverables into tickets.
---

# Convert to Tickets Skill

Turn each deliverable from a PM cycle into a ticket file `docs/tickets/NL-<n>-<slug>.md`. Ticket = requirements carrier (problem, acceptance criteria, constraints, dependencies) + traceability to the deliverable. **No solution spec** — the implementation plan derives the solution later.

## Inputs

One of:

1. **Deliverables index path** — e.g. `.context/pm-cycles/pm-cycle-.../deliverables.md` → convert every deliverable listed
2. **Deliverable file path(s)** — e.g. `.context/pm-cycles/.../deliverables/DEL-01-rotate-key-on-quota.md` → convert only these
3. No input → ask. Do not guess.

## Ticket ID allocation

Sequential `NL-<n>`, counter persisted at `docs/tickets/.next-id` (plain number, next id to use).

- File missing → scan `docs/tickets/NL-*.md`, use `max(existing ids) + 1`. If none exist, start at 100.
- Allocate ids in deliverable order from the index (DEL-01 → lowest new id, etc.).
- After writing all tickets, update `.next-id` to the next unused id.

## Execution

1. **Read the index** (or deliverable files) fully. For each deliverable, capture: DEL id, title, type (feature/bugfix/enhancement), problem, acceptance criteria, constraints, dependencies, source-issue references.
2. **Read existing tickets** in `docs/tickets/` to skip deliverables already converted (a ticket whose traceability block references the same DEL id in the same cycle → skip, note it).
3. **For each deliverable, write** `docs/tickets/NL-<n>-<slug>.md`:

```markdown
# NL-<n> · <feature|bugfix|enhancement> · <Title>

> **Source:** <path-to-deliverable-file> (DEL-xx, pm-cycle-<ts>)
> **Type:** feature | bugfix | enhancement

## Problem

<From deliverable, verbatim or lightly compressed — the WHAT and WHY, user-visible.>

## Acceptance criteria

- [ ] <from deliverable, verbatim>

## Constraints

<From deliverable: stack constraints (no cgo, no new dependency, no Docker), security rules, memory/performance bounds. Omit section if none.>

## Dependencies

<DEL/ticket ids this depends on. "None" if foundational.>

## Notes for planning

<One short paragraph max: pointers the planner may need (related files, prior art, relevant .context/cache/decisions.md entries). NOT a solution.>
```

4. **Update `.next-id`.**
5. **Create per-ticket working folders** (see `.claude/rules/context-cache.md`): `.context/tickets/NL-<n>/` with `ticket.md` = copy of the ticket file. Working state lives under `.context`; `docs/tickets/` holds the committed record.
6. **Report**: table of `NL-<n>` → DEL id → title → path, plus skipped (already converted) entries.

## Rules

- **Requirements only.** If a deliverable contains a solution section (schema, endpoints, UX flows), do NOT copy it into the ticket. Keep problem + AC + constraints. The planner reads the deliverable itself if it wants that context (source link is in the traceability block).
- **Verbatim AC.** Never rewrite acceptance criteria — they are the contract verify-deliverable checks against.
- **Idempotent.** Re-running must not duplicate tickets — the DEL-id traceability check in step 2 is the dedupe key.
- **No git.** Writing files only; no commits (the dev-cycle/handle-ticket flow commits later).
