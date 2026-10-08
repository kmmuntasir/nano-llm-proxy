# Git Guidelines

## Sacred Rule

- **NEVER** run state-changing `git` commands without the user's explicit approval. Read-only inspection (`status`, `diff`, `log`, `show`) is fine.
- `commit` after each task is pre-approved ONLY when the user invokes `/orchestrator`, `/handle-ticket`, `/ticket-pipeline`, or explicitly says "commit per task". Otherwise ask.
- **NEVER** push, merge, rebase, amend, or force-push without explicit per-action approval.

## Branching

- Default/working branch: **`main`**. CI runs on pushes to `main` and on PRs; releases are tagged from `main` (GoReleaser).
- Feature branches: `feat/<slug>`, `fix/<slug>`, `docs/<slug>` (kebab-case slug).
- Local ticket ids (`NL-<n>`, used by the `.context/` pipeline only) may appear in a branch slug but there is no external tracker. Never invent a ticket number.
- Release tagging and merges to `main` are the user's call.

## Commit Messages

History uses conventional-commit prefixes (the release changelog is generated from them):

```
<type>: <subject>
```

- **type:** `feat` | `fix` | `docs` | `test` | `ci` | `security` | `release` | `meta` | `refactor` | `chore` (lowercase).
- **subject:** imperative, ≤72 chars, no trailing period. Lowercase after the prefix, except proper nouns/identifiers.
- Explain *why* in the body (HEREDOC); the diff shows what. One concern per commit.

Examples (from history):

```
fix: make image input work on every path
fix: boot on a fresh install without a legacy keys.json
docs: record the nested limits contract and omp's reasoning caveat
security: redact operator machine details from captured agent fixtures
```

## Workflow

1. Stage explicit paths only — `git add <path1> <path2>`.
2. **Never** `git add -A`, `git add .`, or `git add -u`.
3. Verify with `git status` and `git diff --cached --stat` before committing.
4. Commit with a properly-formatted message (HEREDOC for multi-line).
5. **Never** skip hooks (`--no-verify`).
6. Report commit hash + message + files (`git show --stat --oneline HEAD`). Don't dump diffs.

## Pre-commit Hook Failures

- Do NOT bypass with `--no-verify`.
- Fix the underlying issue, re-stage, create a NEW commit (never `--amend`).
- If a hook fails for reasons outside the task scope, surface it and stop.

## .gitignore Discipline

Never commit (all already in `.gitignore` — don't fight it, and never use `git add -f`):
- `.env`, `keys.json`, `.secrets.md`, `temporary.md`
- `gateway.db`, `gateway.db-wal`, `gateway.db-shm`, `*.bak`
- built binaries (`nano-llm-proxy`, `nano-llm-proxy-linux`, `*.exe`)
- `web/node_modules/`, `web/dist/`, `web/tsconfig.tsbuildinfo`
- `docs/ai-generated/*` (except `.gitkeep`)
- captured fixtures that haven't passed `scripts/sanitize-fixtures.py --check`

## Out of Scope

- Pushing, merging, rebasing, amending, force operations — user's call only.
- Reviewing or modifying the user's in-progress work without confirmation.
- Deleting branches, tags, or remotes without explicit instruction.
