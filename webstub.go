//go:build !prod

package main

import "io/fs"

// webFS returns no filesystem in untagged builds — `go build`/`go test`
// without `-tags prod` need no Node toolchain and no committed dist.
func webFS() (fs.FS, bool) { return nil, false }
