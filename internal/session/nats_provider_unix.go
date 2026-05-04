//go:build !windows

package session

import "syscall"

// processExists reports whether a process with the given PID is still running.
// Unix implementation uses signal 0 (no-op signal that only checks validity).
func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
