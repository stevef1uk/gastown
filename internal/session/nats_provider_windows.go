//go:build windows

package session

import "os"

// processExists reports whether a process with the given PID is still running.
// Windows implementation uses os.FindProcess (always succeeds) combined with
// Release to verify the process handle is valid.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess always succeeds on Windows; we need to check if the
	// process is actually alive by trying to release it.
	return proc.Release() == nil
}
