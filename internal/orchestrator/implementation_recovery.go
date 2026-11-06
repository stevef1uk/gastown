package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// RecoverImplementationStall runs deterministic cleanup when implementation is stuck:
// stop dev servers, reset in_progress implement beads to open, enforce a single in_progress bead.
func RecoverImplementationStall(townRoot, rig string, v WorkflowValidation) (string, error) {
	if rig == "" || townRoot == "" {
		return "", nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	var parts []string
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := runStopRigDevServersScript(mayorRig, v); err != nil {
		if err2 := StopDevServersForRig(v, mayorRig); err2 != nil {
			parts = append(parts, "stop-dev-servers: "+err.Error())
		} else {
			parts = append(parts, "stopped dev servers (fallback)")
		}
	} else {
		parts = append(parts, "stopped dev servers")
	}
	reset, err := ResetInProgressImplementBeads(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(reset) > 0 {
		parts = append(parts, "reset in_progress→open: "+joinStrings(reset, ", "))
	}
	reopened, err := EnforceSingleImplementInProgress(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(reopened) > 0 {
		parts = append(parts, "single in_progress: "+joinStrings(reopened, ", "))
	}
	return joinStrings(parts, "; "), nil
}

// ResetInProgressImplementBeads moves all in_progress implement beads back to open (stall recovery).
func ResetInProgressImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	inProgress, err := listImplementBeadsByStatus(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	if len(inProgress) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var reset []string
	for _, b := range inProgress {
		cmd := exec.Command("bd", "update", b.ID, "--status=open")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return reset, fmt.Errorf("bd update %s: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		reset = append(reset, b.ID)
	}
	return reset, nil
}

func runStopRigDevServersScript(mayorRigDir string, v WorkflowValidation) error {
	return StopDevServersForRig(v, mayorRigDir)
}

func stopRigDevServersScriptPath() string {
	if g := strings.TrimSpace(os.Getenv("GASTOWN")); g != "" {
		p := filepath.Join(g, "scripts", "stop-rig-dev-servers.sh")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Developer layout: freeride gastown next to town root is uncommon; cwd may be gastown repo.
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
			p := filepath.Join(dir, "scripts", "stop-rig-dev-servers.sh")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// ResetImplementationPhase is a soft stall recovery: stop dev servers, reset in_progress→open,
// enforce a single in_progress bead, and clear implementation-progress.json. It does not delete
// on-disk source files (use HardResetImplementationPhase for that).
func ResetImplementationPhase(townRoot, rig string, v WorkflowValidation) (string, error) {
	if rig == "" || townRoot == "" {
		return "", nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string
	if err := runStopRigDevServersScript(mayorRig, v); err != nil {
		if err2 := StopDevServersForRig(v, mayorRig); err2 != nil {
			parts = append(parts, "stop-dev-servers: "+err.Error())
		} else {
			parts = append(parts, "stopped dev servers (fallback)")
		}
	} else {
		parts = append(parts, "stopped dev servers")
	}
	if pruned, err := PruneOpenImplementBeadsForClosedPaths(townRoot, rig, v); err != nil {
		return joinStrings(parts, "; "), err
	} else if len(pruned) > 0 {
		parts = append(parts, "pruned open dupes of closed paths: "+joinStrings(pruned, ", "))
	}
	reset, err := ResetInProgressImplementBeads(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(reset) > 0 {
		parts = append(parts, "reset in_progress→open: "+joinStrings(reset, ", "))
	}
	if picked, err := EnforceSingleImplementInProgress(townRoot, rig, v); err != nil {
		return joinStrings(parts, "; "), err
	} else if len(picked) > 0 {
		parts = append(parts, "single in_progress: "+joinStrings(picked, ", "))
	}
	if cleared, err := ClearImplementationProgressFile(townRoot, rig); err != nil {
		return joinStrings(parts, "; "), err
	} else if cleared {
		parts = append(parts, "cleared implementation-progress.json")
	}
	return joinStrings(parts, "; "), nil
}

// HardResetImplementationPhase deletes on-disk files for open and in_progress implement beads,
// removes malformed layout artifacts, then runs ResetImplementationPhase. Intended for manual
// recovery only — not for automatic wall-clock timeouts.
func HardResetImplementationPhase(townRoot, rig string, v WorkflowValidation) (string, error) {
	if rig == "" || townRoot == "" {
		return "", nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return "", nil
	}
	v = v.ForActivePhase()
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string
	activePaths, err := implementArtifactPathsForActiveBeads(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	removed, err := RemoveImplementBeadArtifactFiles(mayorRig, activePaths)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(removed) > 0 {
		parts = append(parts, "removed active bead files: "+joinStrings(removed, ", "))
	}
	junk, err := RemoveMalformedLayoutArtifactFiles(mayorRig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(junk) > 0 {
		parts = append(parts, "removed malformed artifacts: "+joinStrings(junk, ", "))
	}
	soft, err := ResetImplementationPhase(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if soft != "" {
		parts = append(parts, soft)
	}
	return joinStrings(parts, "; "), nil
}

// ReopenAllPhaseImplementBeads reopens all implement beads in the current active phase
// (not just failed ones). This is an explicit administrative reset only; automatic
// session exhaustion must preserve closed beads and progress.
func ReopenAllPhaseImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	closed, err := listImplementBeadsByStatus(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	if len(closed) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var reopened []string
	for _, b := range closed {
		if b.ID == "" {
			continue
		}
		cmd := exec.Command("bd", "update", b.ID, "--status=open")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return reopened, fmt.Errorf("bd update %s --status=open: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		reopened = append(reopened, b.ID)
	}
	// Clear implementation progress so turn counts are reset
	if _, err := ClearImplementationProgressFile(townRoot, rig); err != nil {
		// Non-fatal, just log
	}
	return reopened, nil
}

// resetBeadTurnCount sets the turn count for a bead back to 0 in the implementation progress file.
func resetBeadTurnCount(townRoot, rig, beadID string) error {
	if townRoot == "" || rig == "" || beadID == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "qa", "implementation-progress.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var progress map[string]json.RawMessage
	if err := json.Unmarshal(data, &progress); err != nil {
		return fmt.Errorf("invalid implementation progress: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("invalid null implementation progress")
	}
	turns := map[string]int{}
	if raw, ok := progress["bead_turns"]; ok {
		if err := json.Unmarshal(raw, &turns); err != nil {
			return err
		}
	}
	delete(turns, beadID)
	raw, err := json.Marshal(turns)
	if err != nil {
		return err
	}
	progress["bead_turns"] = raw
	updated, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".implementation-progress-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(updated); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)

}

// ClearImplementationProgressFile removes qa/implementation-progress.json so polecat does not skip verify.
func ClearImplementationProgressFile(townRoot, rig string) (bool, error) {
	if townRoot == "" || rig == "" {
		return false, nil
	}
	path := filepath.Join(townRoot, rig, "qa", "implementation-progress.json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
