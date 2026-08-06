// +build windows

package orchestrator

import (
	"os/exec"
)

// detachCmd is a no-op on Windows since it doesn't support Setsid.
func detachCmd(cmd *exec.Cmd) {
	// Windows doesn't support Setsid
}