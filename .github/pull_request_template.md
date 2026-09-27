## What this changes

<!-- One or two sentences. What is different after this merges? -->

## Why

<!-- The problem, not the solution. Link an issue if there is one: Fixes #123 -->

## How it was verified

<!-- What you ran or clicked. If it is testable, say what the test asserts. -->

```bash
go vet ./... && go test -race ./...
(cd web && npm run build)
```

- [ ] `go vet`, `gofmt`, and `go test -race ./...` pass
- [ ] The GUI builds if I touched `web/`
- [ ] No new runtime dependency, or the PR description argues for it
- [ ] The README and `docs/` still match the behaviour
- [ ] I did not add network access, live API keys, or a live provider
      dependency to the test suite

<!--
If this changes the security surface — auth, authorization, key handling, the
web tools' fetch path — say so explicitly here and describe the threat model
you considered. That change gets reviewed more carefully than the rest.
-->
