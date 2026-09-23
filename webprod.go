//go:build prod

package main

import (
	"embed"
	"io/fs"
	"log"
)

// webDist holds the built admin GUI (web/dist, produced by `npm run build`).
// Only embedded under the `prod` build tag so `go test ./...` never needs the
// Node toolchain.
//
//go:embed all:web/dist
var webDist embed.FS

func webFS() (fs.FS, bool) {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		log.Fatalf("embed web/dist: %v", err)
	}
	return sub, true
}
