//go:build windows

package util

import "errors"

// FindPsBinary on Windows returns a placeholder.
// Process checking on Windows uses different APIs; this is a stub
// for cross-compilation compatibility.
func FindPsBinary() string {
	return "tasklist"
}

// OrphanedProcess represents a claude process running without a controlling terminal.
type OrphanedProcess struct {
	PID      int
	Cmd      string
	Age      int
	TownRoot string
}

// CleanupResult describes what happened to an orphaned process.
type CleanupResult struct {
	Process OrphanedProcess
	Signal  string
	Error   error
}

// ZombieProcess represents a claude process not in any active tmux session.
type ZombieProcess struct {
	PID      int
	Cmd      string
	Age      int
	TTY      string
	TownRoot string
}

// ZombieCleanupResult describes what happened to a zombie process.
type ZombieCleanupResult struct {
	Process ZombieProcess
	Signal  string
	Error   error
}

// FindOrphanedClaudeProcesses is a no-op on Windows.
func FindOrphanedClaudeProcesses() ([]OrphanedProcess, error) {
	return nil, nil
}

// FindZombieClaudeProcesses is a no-op on Windows.
func FindZombieClaudeProcesses() ([]ZombieProcess, error) {
	return nil, nil
}

// CleanupZombieClaudeProcesses is a no-op on Windows.
func CleanupZombieClaudeProcesses() ([]ZombieCleanupResult, error) {
	return nil, nil
}

// CleanupOrphanedClaudeProcesses is a no-op on Windows.
func CleanupOrphanedClaudeProcesses() ([]CleanupResult, error) {
	return nil, errors.New("orphan cleanup not supported on Windows")
}
