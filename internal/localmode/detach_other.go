//go:build !unix && !windows

package localmode

import "os/exec"

// detach has no session/console concept to set on this platform.
func detach(*exec.Cmd) {}

// hideConsole has no console window to hide on this platform.
func hideConsole(*exec.Cmd) {}
