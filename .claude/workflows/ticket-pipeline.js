// ticket-pipeline Dynamic Workflow
//
// Implements the batch ticket-processing pipeline as a Claude Code Dynamic
// Workflow (CLI v2.1.154+). Each "parent agent" from the original diagram
// (orchestrator, ticket-handler, planner, task-breakdown-agent, implementer,
// verifier) becomes a STAGE in this script. The actual subagents (analyst,
// go-coder, react-coder, committer) are spawned FLAT via agent().
//
// Invocation:
//   /ticket-pipeline <path-to-ticket-list>
//   /ticket-pipeline "<inline ticket list as text>"
//
// Pipeline (per ticket):
//   curate -> plan -> breakdown -> implement (parallel when disjoint) -> verify -> commit
//
// Stack assumptions (nano-llm-proxy):
//   - Layer "backend" = the Go gateway: Go + SQLite (main.go + internal/*;
//               pure Go, no cgo, no Docker; in-code migrateVn() migrations;
//               offline tests against mock upstreams)
//   - Layer "frontend" = the admin GUI in web/: React 19 + TypeScript + Vite +
//               Chakra UI v3 + TanStack Query + react-router-dom (no test
//               framework — `npm run build` is the gate)
//   - Commits:  `<type>: <subject>` (feat/fix/docs/test/ci/security/release/
//               meta/refactor/chore); ticket id (NL-<n>) goes in the body.
//
// Script-layer constraints (Dynamic Workflows):
//   - Plain JavaScript only (no TypeScript syntax).
//   - No Date.now(), Math.random(), or argless new Date() — they throw.
//   - No filesystem or shell access from this layer. All I/O via agent() calls.
//   - Hard 1-level cap on nested workflow() calls (we don't nest here).
//   - parallel() failed thunk returns null; always guard.
//   - Subagents run in acceptEdits mode; file writes auto-apply.

const SCHEMAS = {
  taskList: {
    type: 'array',
    items: {
      type: 'object',
      required: ['id', 'title', 'layer'],
      properties: {
        id: { type: 'string' },
        title: { type: 'string' },
        layer: { type: 'string', enum: ['backend', 'frontend', 'fullstack', 'other'] },
        files: { type: 'array', items: { type: 'string' } },
        criteria: { type: 'string' },
        deps: { type: 'array', items: { type: 'string' } },
      },
    },
  },
  plan: {
    type: 'object',
    required: ['summary', 'approach', 'filesTouched'],
    properties: {
      summary: { type: 'string' },
      approach: { type: 'string' },
      risks: { type: 'array', items: { type: 'string' } },
      filesTouched: { type: 'array', items: { type: 'string' } },
      openQuestions: { type: 'array', items: { type: 'string' } },
    },
  },
  breakdown: {
    type: 'object',
    required: ['tasks'],
    properties: {
      tasks: {
        type: 'array',
        items: {
          type: 'object',
          required: ['id', 'title', 'layer'],
          properties: {
            id: { type: 'string' },
            title: { type: 'string' },
            layer: { type: 'string', enum: ['backend', 'frontend', 'other'] },
            files: { type: 'array', items: { type: 'string' } },
            criteria: { type: 'string' },
            deps: { type: 'array', items: { type: 'string' } },
          },
        },
      },
    },
  },
  report: {
    type: 'object',
    required: ['verified', 'gaps'],
    properties: {
      verified: { type: 'boolean' },
      gaps: { type: 'array', items: { type: 'string' } },
      notes: { type: 'string' },
      filesTouched: { type: 'array', items: { type: 'string' } },
    },
  },
  commit: {
    type: 'object',
    required: ['committed'],
    properties: {
      committed: { type: 'boolean' },
      sha: { type: 'string' },
      message: { type: 'string' },
      files: { type: 'array', items: { type: 'string' } },
    },
  },
};

// --- Prompt builders --------------------------------------------------------

function curatePrompt(ticketList) {
  return [
    'You are the analyst subagent in the ticket-pipeline workflow.',
    'Think like a senior engineer triaging a ticket list. Do NOT think like a product manager.',
    '',
    'Read the ticket list below. For each ticket, return structured JSON that the',
    'downstream planner stage will consume. Resolve obvious scope questions by',
    'inspecting the codebase. Strip out anything that needs a human decision.',
    '',
    'Ticket list (path or inline content):',
    '---',
    ticketList,
    '---',
    '',
    'Return JSON matching this shape:',
    '[{',
    '  "id":         "NL-2054",               // ticket id; omit if none',
    '  "title":      "short imperative",',
    '  "layer":      "backend|frontend|fullstack|other",   // backend = Go gateway, frontend = web/ GUI',
    '  "files":      ["paths/that/will/change"],   // best guess',
    '  "criteria":   "one-line acceptance criteria",',
    '  "deps":       ["NL-2053"]              // other ticket ids that must land first',
    '}]',
    '',
    'Do not include preamble or markdown fences. Just the JSON array.',
  ].join('\n');
}

function plannerPrompt(task) {
  return [
    'You are the planner subagent in the ticket-pipeline workflow.',
    'Read the ticket below, investigate the codebase, and produce an implementation plan.',
    'Think like a senior engineer. Surface file paths, approach, risks, and open questions.',
    '',
    'Follow the approach documented in the `/create-implementation-plan` skill',
    '(see .claude/skills/create-implementation-plan/SKILL.md). If you can invoke',
    'skills, run that skill against the ticket; otherwise read the SKILL.md and',
    'apply its phases (read ticket -> analyze codebase -> write plan).',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Return JSON matching this shape:',
    '{',
    '  "summary":       "one-paragraph plan",',
    '  "approach":      "step-by-step, ordered",',
    '  "risks":         ["..."],',
    '  "filesTouched":  ["exact paths"],',
    '  "openQuestions": ["only blockers; resolve obvious ones via the codebase"]',
    '}',
    '',
    'Do not include preamble or markdown fences. Just the JSON object.',
  ].join('\n');
}

function breakdownPrompt(task, plan) {
  return [
    'You are the task-breakdown subagent in the ticket-pipeline workflow.',
    'Convert the plan into small, parallelizable tasks that an individual coder',
    '(go-coder for the Go gateway, or react-coder for the web/ GUI) can implement in one shot.',
    '',
    'Follow the two-phase process documented in the `/breakdown-plan-into-tasks`',
    'skill (see .claude/skills/breakdown-plan-into-tasks/SKILL.md): codebase',
    'analysis first, then task list. Invoke the skill if you can; otherwise read',
    'the SKILL.md and apply its phases.',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Plan:',
    JSON.stringify(plan, null, 2),
    '',
    'Return JSON matching this shape:',
    '{',
    '  "tasks": [{',
    '    "id":       "t1",',
    '    "title":    "imperative",',
    '    "layer":    "backend|frontend|other",',
    '    "files":    ["exact paths this task touches"],',
    '    "criteria": "acceptance",',
    '    "deps":     ["other task ids in this breakdown"]',
    '  }]',
    '}',
    '',
    'Split backend (Go gateway) vs frontend (web/ React GUI) cleanly.',
    'Tasks touching the same file must declare deps.',
    'Do not include preamble or markdown fences. Just the JSON object.',
  ].join('\n');
}

function implementPrompt(task, breakdown) {
  return [
    'You are the implementer subagent in the ticket-pipeline workflow.',
    'Implement every task in the breakdown. Follow project conventions:',
    '- Backend (Go gateway): Go + SQLite, main.go + internal/{config,gateway,settings,store,webtools}; pure Go, no cgo,',
    '  no Docker, no new direct dependency without justification; handlers thin, errors via apiErr ({"error":{"message":...}});',
    '  schema changes as a new in-code migrateVn() in internal/store/store.go; parameterized SQL only;',
    '  tests are *_test.go beside the code, offline against scripted mock upstreams; gofmt formatting.',
    '- Frontend (web/): React 19 + TypeScript + Vite + Chakra UI v3 + TanStack Query + react-router-dom; requests via',
    '  web/src/api/client.ts; types in web/src/api/types.ts. NO test framework — `npm run build` is the gate.',
    '- Commit convention: `<type>: <subject>` for any commit you do NOT make here (a later stage commits).',
    '- PascalCase component files (match web/src/components), camelCase functions, Go idioms on the backend.',
    '- No `any` in TS; explicit prop types; functional components only; `import type` for type-only imports.',
    '- No `console.log` in production paths; no string-concatenated SQL; never log or return upstream keys.',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Breakdown:',
    JSON.stringify(breakdown, null, 2),
    '',
    'Write the code. Run the relevant checks if possible:',
    '  backend:  go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"',
    '  frontend: cd web && npm run build',
    'Return a short summary of what you changed (file paths + one line each)',
    'and any blocker you hit. Do NOT commit — a later stage handles that.',
  ].join('\n');
}

function backendImplementPrompt(task, breakdown) {
  const backend = {
    tasks: breakdown.tasks.filter((t) => t.layer === 'backend'),
  };
  return [
    'You are the BACKEND implementer subagent. Go gateway only (main.go + internal/*).',
    'Do not touch web/ files.',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Backend tasks:',
    JSON.stringify(backend, null, 2),
    '',
    'Conventions: pure Go, no cgo, no Docker, no new direct dependency unless the task demands it (5 today);',
    'handlers in internal/gateway (admin API api_*.go, routes in routes.go), persistence in internal/store,',
    'runtime knobs in internal/settings, bootstrap env in internal/config (+ .env.example, docs/configuration.md);',
    'admin errors via apiErr -> {"error":{"message":...}}; schema changes ONLY as a new in-code migrateVn()',
    'recorded in schema_migrations (never edit an applied one); parameterized SQL only; client keys stay hashed,',
    'upstream keys never logged or returned after creation; URL-fetching code goes through the SSRF guard;',
    'memory-conscious (512 MB VPS target); tests are *_test.go beside the code, offline, against mock upstreams;',
    'gofmt formatting; comments explain why, matching surrounding density.',
    '',
    'Write the code. Run `go vet ./... && go test -race ./... && test -z "$(gofmt -l .)"` if possible.',
    'Return file paths changed + one line each. Do NOT commit.',
  ].join('\n');
}

function frontendImplementPrompt(task, breakdown) {
  const frontend = {
    tasks: breakdown.tasks.filter((t) => t.layer === 'frontend'),
  };
  return [
    'You are the FRONTEND implementer subagent. The admin GUI in web/: React 19 + TypeScript + Vite +',
    'Chakra UI v3 (wrappers in src/components/ui) + TanStack Query + react-router-dom. No additional UI,',
    'styling, state, or data-fetching libraries. Do not touch Go files.',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Frontend tasks:',
    JSON.stringify(frontend, null, 2),
    '',
    'Conventions: match existing file naming in web/src; functional components + hooks; explicit prop types (no `any`);',
    'requests only through web/src/api/client.ts (api/post/put/patch/del) with types in web/src/api/types.ts mirroring',
    'the gateway JSON exactly; server data via useQuery/useMutation with stable queryKeys and invalidation after',
    'mutations; auth is cookie-based (no tokens in localStorage), 401 handled centrally; errors surfaced through the',
    'toaster; reuse DataTable / ConfirmDialog / CopyButton / KeyRevealDialog / StatusBadge; destructive actions',
    'confirmed via ConfirmDialog; routes in src/App.tsx (admin-only pages stay under the role check);',
    '`import type` for type-only imports; noUnusedLocals/Parameters are build-breaking.',
    '',
    'Write the code. Run `cd web && npm run build` if possible.',
    'Return file paths changed + one line each. Do NOT commit.',
  ].join('\n');
}

function verifyPrompt(task, breakdown, implSummary) {
  return [
    'You are the verifier subagent in the ticket-pipeline workflow.',
    'Check the implementation against the breakdown. Report gaps honestly.',
    '',
    'Follow the `/verify-deliverable` skill',
    '(see .claude/skills/verify-deliverable/SKILL.md): check acceptance criteria',
    'from the ticket against the implementation (intent conformance) and the',
    'plan/breakdown against the implementation (plan conformance). Invoke the',
    'skill if you can; otherwise read the SKILL.md and apply its method.',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Breakdown:',
    JSON.stringify(breakdown, null, 2),
    '',
    'Implementation summary from coder:',
    implSummary,
    '',
    'Return JSON:',
    '{',
    '  "verified":     true|false,',
    '  "gaps":         ["specific missing piece or path"],',
    '  "notes":        "free-form",',
    '  "filesTouched": ["paths you observed changed"]',
    '}',
    '',
    'Mark verified=false if ANY acceptance criterion from the breakdown is unmet.',
    'Do not include preamble or markdown fences.',
  ].join('\n');
}

function commitPrompt(task, report) {
  const ticketId = typeof task.id === 'string' ? task.id.trim() : '';
  return [
    'You are the committer subagent in the ticket-pipeline workflow.',
    'Commit the implementation for this single ticket. Project conventions:',
    '- Commit format: `<type>: <subject>` with type one of feat|fix|docs|test|ci|security|release|meta|refactor|chore (e.g. `fix: stop cooldown reset on probe success`).',
    '- No ticket id in the subject; if the ticket has an NL-<n> id, add a `Ref: NL-<n>` line in the body.',
    '- subject: imperative, lowercase, <=72 chars, no trailing period.',
    '- Stage ONLY the files in report.filesTouched. Never `git add .`.',
    '- NEVER push, merge, rebase, amend, or sign.',
    '- NEVER skip hooks (--no-verify).',
    '',
    'Ticket:',
    JSON.stringify(task, null, 2),
    '',
    'Verifier report:',
    JSON.stringify(report, null, 2),
    '',
    'Stage the files in report.filesTouched (skip if verified=false) and commit',
    'with a "<type>: <lowercase imperative subject>" message'+(ticketId ? ' and a body line "Ref: ' + ticketId + '"' : '') + '.',
    '',
    'Return JSON:',
    '{',
    '  "committed": true|false,',
    '  "sha":       "short sha if committed",',
    '  "message":   "the commit message",',
    '  "files":     ["staged paths"]',
    '}',
    '',
    'If verified=false, return committed=false and explain in `message`.',
  ].join('\n');
}

// --- Helpers ------------------------------------------------------------

function canParallelize(breakdown) {
  if (!breakdown || !Array.isArray(breakdown.tasks)) return false;
  const hasBackend = breakdown.tasks.some((t) => t.layer === 'backend');
  const hasFrontend = breakdown.tasks.some((t) => t.layer === 'frontend');
  if (!(hasBackend && hasFrontend)) return false;

  // Disjoint-file check across layers
  const seen = new Map();
  for (const t of breakdown.tasks) {
    for (const f of t.files || []) {
      const prev = seen.get(f);
      if (prev && prev !== t.layer) return false;
      seen.set(f, t.layer);
    }
  }
  return true;
}

function summarizeTask(task, plan, breakdown, impl, report, commit) {
  const id = task.id || '(no-id)';
  const status = report && report.verified ? 'DONE' : 'GAPS';
  const lines = [
    '## ' + id + ' — ' + (task.title || '') + ' [' + status + ']',
    '',
    plan ? '- Plan: ' + (plan.summary || '').slice(0, 140) : '',
    breakdown ? '- Tasks: ' + (breakdown.tasks || []).length + ' subtask(s)' : '',
    report ? '- Verified: ' + (report.verified ? 'yes' : 'NO — ' + ((report.gaps || []).join('; '))) : '',
    commit ? '- Commit: ' + (commit.committed ? commit.sha + ' — ' + commit.message : 'NOT COMMITTED: ' + commit.message) : '',
  ];
  return lines.filter(Boolean).join('\n');
}

// --- Workflow entry -----------------------------------------------------

export default async function* (input, api) {
  const { agent, parallel, pipeline, phase, log } = api;

  const ticketList =
    typeof input === 'string' ? input : input && (input.ticketList || input.tickets) || '';

  if (!ticketList) {
    return 'Usage: /ticket-pipeline <path-to-ticket-list | "inline ticket text">';
  }

  // Stage 1: curate
  phase('Curating ticket list');
  log('Reading ticket list and resolving obvious scope questions');
  const tasks = yield agent(curatePrompt(ticketList), { schema: SCHEMAS.taskList });

  if (!tasks || tasks.length === 0) {
    return 'No tickets to process.';
  }

  log('Processing ' + tasks.length + ' ticket' + (tasks.length === 1 ? '' : 's'));

  // Stages 2-6 run as a pipeline so ticket A can be in verify while ticket B
  // is still in plan. Each stage yields one agent() result per ticket.
  const perTicketResults = yield pipeline(
    tasks,
    // Stage 2: plan
    (task) => agent(plannerPrompt(task), { schema: SCHEMAS.plan }),
    // Stage 3: breakdown
    (plan, task) => agent(breakdownPrompt(task, plan), { schema: SCHEMAS.breakdown }),
    // Stage 4: implement (parallel backend+frontend when safe, else single dispatch)
    (breakdown, plan, task) => {
      if (!canParallelize(breakdown)) {
        return agent(implementPrompt(task, breakdown));
      }
      log('Ticket ' + (task.id || '(no-id)') + ': parallelizing backend + frontend');
      return parallel([
        () => agent(backendImplementPrompt(task, breakdown)),
        () => agent(frontendImplementPrompt(task, breakdown)),
      ]).then((parts) => parts.filter(Boolean).join('\n\n---\n\n'));
    },
    // Stage 5: verify
    (impl, breakdown, plan, task) =>
      agent(verifyPrompt(task, breakdown, impl), { schema: SCHEMAS.report }),
    // Stage 6: commit
    (report, impl, breakdown, plan, task) =>
      agent(commitPrompt(task, report), { schema: SCHEMAS.commit })
  );

  // perTicketResults is an array aligned with `tasks`; each entry is the final
  // stage's result for that ticket, with prior stage results available as
  // deeper array indices. For readability, we synthesize a per-task summary.
  const summary = tasks.map((task, i) => {
    const chain = Array.isArray(perTicketResults[i])
      ? perTicketResults[i]
      : [perTicketResults[i]];
    const [plan, breakdown, impl, report, commit] = Array.isArray(chain[0])
      ? chain[0]
      : chain;
    return summarizeTask(task, plan, breakdown, impl, report, commit);
  });

  phase('Done');
  return '# ticket-pipeline run summary\n\n' + summary.join('\n\n');
}
