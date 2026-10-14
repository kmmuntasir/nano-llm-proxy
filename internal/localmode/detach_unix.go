//go:build unix

package localmode

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session (setsid) so it survives the
// installer's terminal closing and is not hit by its job-control signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// hideConsole is a no-op off Windows: there is no console window to flash.
func hideConsole(*exec.Cmd) {}
