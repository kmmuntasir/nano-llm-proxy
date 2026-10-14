package main

import _ "embed"

// changelogMD is CHANGELOG.md, embedded so the GUI's About page shows the
// notes of exactly the build that is running (every build, not only -tags
// prod — it is a few kilobytes).
//
//go:embed CHANGELOG.md
var changelogMD string
