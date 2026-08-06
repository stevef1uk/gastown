// +build !windows

package orchestrator

import (
	"os/exec"
	"syscall"
)

// detachCmd detaches the command from the parent process on Unix-like systems.
func detachCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}