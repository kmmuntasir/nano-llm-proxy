---
name: breakdown-plan-into-tasks
description: Break a large implementation plan into small, parallelizable tasks for individual developers. Use when user requests to break down a plan into tasks.
---

# Task Breakdown Skill

Read provided plan file. Then follow two-phase process below.

## Phase 1: Codebase Analysis

Before breaking down tasks, **analyze the codebase** to build accurate understanding and fill knowledge gaps plan may leave implicit. Prevents tasks referencing nonexistent files, wrong abstractions, or outdated patterns.

Specifically:

- **Verify every file path and module** plan mentions — confirm they exist, or note they need creation
- **Map current architecture** — understand existing patterns, conventions, and interfaces plan builds on
- **Identify hidden coupling** — shared types, utilities, or config plan doesn't explicitly call out but tasks will touch
- **Check for prior art** — search for existing implementations that partially cover what plan describes (partial refactor, abandoned branch, utility function already written)

Use up to **3 parallel subagents** (via Agent tool) to speed up this phase and keep main context window clean. Example split:

| Subagent | Responsibility |
|----------|---------------|
| Subagent 1 | Verify file/module existence, map directory structure, check build/config files |
| Subagent 2 | Trace data flow and interfaces the plan references — read relevant source files |
| Subagent 3 | Search for prior art, existing utilities, and hidden coupling across the codebase |

Each subagent should return concise summary of findings. Use those summaries — not raw exploration — to inform breakdown.

## Phase 2: Task Breakdown

Using plan plus codebase analysis, break work into small, self-contained tasks individual developers can pick up independently.

Continue using up to **3 parallel subagents** during this phase to draft batches of tasks concurrently. Example:

| Subagent | Responsibility |
|----------|---------------|
| Subagent 1 | Draft Batch 1 tasks (no dependencies) |
| Subagent 2 | Draft Batch 2 tasks (depends on Batch 1) |
| Subagent 3 | Draft Batch 3+ tasks and the visual dependency diagram |

Merge subagent outputs into final document, resolving conflicts or gaps.

## Output

Write results in new file alongside plan, named `{plan-filename}-tasks.md`.

## Task Format

Each task must include:

- **Title** — concise, action-oriented (e.g., "Extract WebSocket manager into ES module")
- **Description** — detailed enough for developer unfamiliar with plan to execute. Include source references (file paths, line numbers, function names, codeblocks), what to create/modify, and relevant context
- **Acceptance Criteria** — specific, verifiable checklist items
- **Subtasks** — only if task complex enough to warrant
- **Dependencies** — exact task numbers this depends on, or "None"

## Parallelization Strategy

Include batch-based execution model at top of document:

1. **Organize tasks into batches** by dependency order — all tasks within batch can run in parallel with zero merge conflicts
2. **Include a visual batch diagram** showing dependency flow
3. **State merge order rules** — which batches must merge before others can start
4. **Provide a summary table** — columns: `#`, Batch, Target File, Dependencies, Can Parallel With
5. **Suggest developer assignment tracks** — 2–3 developer paths through batches

## Key Principles

- **One task = only a few files (tightly coupled set)** — minimize merge conflict surface
- **Dependencies are explicit** — every dependency listed by task number