//go:build windows

package localmode

import (
	"os/exec"
	"syscall"
)

// Process creation flags not exported by package syscall.
const (
	detachedProcess = 0x00000008
	createNoWindow  = 0x08000000
)

// detach starts the child without a console and in its own process group,
// so closing the installer's console neither shows a window nor kills it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: detachedProcess | syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// hideConsole stops reg/tasklist/taskkill from flashing a console window
// when called from the GUI-subsystem build (nano-llm-proxyw.exe).
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
