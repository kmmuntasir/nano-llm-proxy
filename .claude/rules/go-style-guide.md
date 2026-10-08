# Go Style & Testing Guide (backend)

## Formatting

- `gofmt` is the formatter; CI fails on `gofmt -l .` output. Tabs (see `.editorconfig`), LF, UTF-8.
- `go vet ./...` must be clean.
- Match the surrounding file; no linter beyond vet/gofmt is configured.

## Naming

- Packages: short, lowercase, single word (`store`, `gateway`, `webtools`).
- Exported only what other packages need; handlers and helpers are unexported (`handleListUsers`, `writeJSON`).
- Files: one concern per file, snake-free lowercase (`api_users.go`, `providerspec.go`); tests `xxx_test.go` beside the code.
- Test functions: `TestThing_behavior` or the existing `TestThingDoesX` style of the file; table tests use `t.Run`.
- Acronyms keep case (`ID`, `URL`, `SSE`, `MCP`).

## Language & stdlib

- Use the Go version in `go.mod` freely (generics, `slices`, `maps`, `min`/`max`, range-over-int, enhanced `ServeMux`).
- `any` over `interface{}`. `errors.Is/As`, `%w` wrapping. `context.Context` first param on anything that does I/O.
- Prefer the standard library over a dependency. `log` for logging (no `fmt.Println` in non-CLI paths).
- Comments explain *why* / contracts (see doc comments on `Config`, `readJSON`); don't restate code. Exported identifiers get doc comments.
- Short functions, early returns; `if err != nil { return … }` — no panics for expected failures.
- No global mutable state beyond what `gateway` already owns; inject collaborators via struct fields.

## Testing

- Standard `testing` package only — no testify/gomock. Keep the dependency graph at five.
- Tests are offline: scripted mock upstreams via `httptest.NewServer`, temp-file SQLite via `openTestStore(t)`, helpers like `testStoreGateway`. A test needing a live provider is a test nobody can run.
- Fixtures under `internal/gateway/testdata/` are reference only; tests build their own inline data. Captured traffic must stay sanitized (`scripts/sanitize-fixtures.py --check`).
- Cover the failure paths that define this project: 401 vs 429 vs 5xx classification, cooldown/blacklist transitions, failover before/after first byte, Origin rejection, per-user provider revocation, SSRF blocks.
- Always `t.Helper()` in helpers, `t.Cleanup`/`t.TempDir()` for resources; no sleeps for synchronization where a channel/clock seam works; no ordering assumptions between tests.
- Run `go test -race ./...` — concurrency bugs are the realistic ones here.

## Running

```bash
go test ./...                               # fast loop
go test -race ./...                         # the gate
go test ./internal/gateway -run TestName    # one test
go vet ./... && gofmt -l .
```

## Avoid

- cgo, `unsafe`, reflection tricks, init-time side effects.
- Swallowed errors, `log.Fatal` outside `main`, logging secrets.
- Adding test frameworks or network-dependent tests.
- Premature interfaces: define one only where a second implementation or a test seam exists.
