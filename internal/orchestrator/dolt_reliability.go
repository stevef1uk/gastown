package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// ensureDoltAutoCommit sets dolt.auto-commit=on in the rig's beads config.
// This ensures bd close persists immediately (the BD_DOLT_AUTO_COMMIT env var
// is not recognized by bd — only the config key or --dolt-auto-commit flag works).
func ensureDoltAutoCommit(townRoot, rig string) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if beadsDir == "" {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	cmd := exec.Command("bd", "config", "set", "dolt.auto-commit", "on")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd config set dolt.auto-commit on: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// commitDoltWorkingSet commits pending Dolt changes for a rig's beads database.
// This ensures the SQL server sees bead state changes made with BD_DOLT_AUTO_COMMIT=off.
func commitDoltWorkingSet(townRoot, rig string) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if beadsDir == "" {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	cmd := exec.Command("bd", "dolt", "commit", "-m", "reconcile: auto-commit bead state changes")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// "nothing to commit" is not an error
		if strings.Contains(string(out), "nothing to commit") || strings.Contains(string(out), "no changes") {
			return nil
		}
		return fmt.Errorf("bd dolt commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureDoltSchemaHealth checks that bd list works for a rig's beads database.
// If bd list fails with a schema error (e.g., missing started_at column), it
// attempts to fix the issue by committing pending Dolt changes.
func ensureDoltSchemaHealth(townRoot, rig string) error {
	if townRoot == "" || rig == "" {
		return nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if beadsDir == "" {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	// Check if bd list works
	cmd := exec.Command("bd", "list", "--status=open", "--json", "--limit=0")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil // bd list works fine
	}

	output := strings.ToLower(string(out))
	// Check for schema errors
	if strings.Contains(output, "could not be found in any table") ||
		strings.Contains(output, "started_at") ||
		strings.Contains(output, "column") ||
		strings.Contains(output, "error 1105") {
		// Attempt to fix by committing pending Dolt changes
		if commitErr := commitDoltWorkingSet(townRoot, rig); commitErr == nil {
			// Retry bd list after commit
			cmd2 := exec.Command("bd", "list", "--status=open", "--json", "--limit=0")
			cmd2.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
			cmd2.Dir = rigDir
			if _, retryErr := cmd2.CombinedOutput(); retryErr == nil {
				return nil // fixed by commit
			}
		}
		// If commit didn't work, try bd doctor --fix
		doctorCmd := exec.Command("bd", "doctor", "--fix")
		doctorCmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		doctorCmd.Dir = rigDir
		doctorCmd.CombinedOutput() // best-effort
	}
	return nil
}

// bdCommandWithRetry runs a bd command and retries on transient Dolt errors.
// Returns the combined output and error from the final attempt.
func bdCommandWithRetry(townRoot, rig string, args []string) ([]byte, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	cmd := exec.Command("bd", args...)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}

	output := strings.ToLower(string(out))
	// Check for transient Dolt errors
	if strings.Contains(output, "could not be found in any table") ||
		strings.Contains(output, "error 1105") ||
		strings.Contains(output, "connection refused") ||
		strings.Contains(output, "server lost") {
		// Try committing first
		_ = commitDoltWorkingSet(townRoot, rig)
		// Retry
		cmd2 := exec.Command("bd", args...)
		cmd2.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd2.Dir = rigDir
		out2, err2 := cmd2.CombinedOutput()
		if err2 == nil {
			return out2, nil
		}
		return out2, err2
	}
	return out, err
}
