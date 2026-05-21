package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	script := stopRigDevServersScriptPath()
	if script == "" {
		return StopDevServersForRig(v, mayorRigDir)
	}
	port := "8080"
	for p := range buildDevServerTracker(v, mayorRigDir).ports {
		if p > 0 && !protectedDevPorts[p] {
			port = strconv.Itoa(p)
			break
		}
	}
	cmd := exec.Command("bash", script, port)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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
