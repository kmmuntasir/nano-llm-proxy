# Contributing to nano-llm-proxy

Thanks for considering it. This is a small, dependency-obsessed project, and
the bar for a change is mostly "does it keep the dependency graph that small,
and does it keep working without a network?"

## Getting set up

```bash
git clone https://github.com/kmmuntasir/nano-llm-proxy.git
cd nano-llm-proxy

# The GUI is embedded only under the `prod` build tag, so building it is
# optional for backend work — but `deploy.sh` needs it.
(cd web && npm ci && npm run dev)     # admin GUI on :5173, proxying to :8787

go run .                              # gateway on :8787
```

You need Go (see `go.mod`) and, for the GUI, Node 22+.

First boot needs an admin account. Either export the credentials or copy
`.env.example` to `.env` and fill them in:

```bash
export ADMIN_EMAIL=you@example.com ADMIN_PASSWORD=...
go run .
```

## Before you open a PR

```bash
go vet ./...
go test -race ./...
gofmt -l .            # must print nothing
(cd web && npm run build)   # runs tsc -b, so this catches type errors
```

CI runs exactly these, plus a static-binary build and a check that the captured
fixtures stay sanitized. All of it runs without network access, API keys, or a
Node toolchain for the backend job — please keep it that way. A test that
needs a live provider is a test nobody can run.

## House rules

**The dependency graph is a feature.** Five direct Go dependencies, all pure
Go, no cgo. Adding one is a real cost and needs a justification in the PR
description. Prefer a few lines of local code to a new module, and prefer the
standard library to both.

**Fixtures are reference material, not test inputs.** No Go test reads
`internal/gateway/testdata/`. It exists so the provider adapters can be worked
on offline, and it is captured from real traffic — which means it can contain
machine details that have no business being public. If you record new fixtures
with `scripts/capture-zai-fixtures.sh`, the redaction runs automatically;
`scripts/sanitize-fixtures.py --check` in CI will catch you if it didn't.

**Don't widen the security surface casually.** The gateway holds upstream
provider keys in plaintext by necessity, has no built-in TLS, and is designed
to bind to loopback. Those are deliberate trade-offs, documented in
[SECURITY.md](SECURITY.md). If a change makes any of them worse, call it out
explicitly rather than slipping it in.

**Preserve the honest docs.** The README has a section on where this project
is deliberately *not* the right choice. If your change closes one of those
gaps, update that section. If it doesn't, don't quietly delete it.

## Commit and PR style

The history uses conventional-commit prefixes — `feat:`, `fix:`, `docs:`,
`test:`, `ci:`, `security:` — because the release changelog is generated from
them. Match the surrounding style and explain *why* in the body; the diff
already shows what changed.

Keep a PR to one concern. A refactor riding along with a feature is harder to
review and harder to revert.

## Good first issues

- A provider preset for an upstream not on the list
- A transcript for a provider that has a non-standard catalog, so its
  metadata mapping can be worked on offline
- Documentation for a deployment shape not yet covered in `docs/`
- A test for a path that currently has none — the SSRF guard, the failure
  classifier, and the cooldown math are the interesting ones

## Reporting security issues

Not as a public issue. See [SECURITY.md](SECURITY.md).

## License

By contributing you agree that your contributions are licensed under the
project's GPL-3.0-or-later license, the same as the rest of the project.
