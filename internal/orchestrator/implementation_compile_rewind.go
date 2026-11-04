package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func completedPhaseGoCompileFailure(rigDir string, v WorkflowValidation) (int, []string, string) {
	active := v.ActivePhaseIndex()
	if !WorkflowUsesGo(v) || !v.HasPhasedDelivery() || active <= 0 {
		return -1, nil, ""
	}
	moduleDir := filepath.Join(rigDir, v.LayoutRootDir())
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return -1, nil, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", os.DevNull, "./...")
	cmd.Dir = moduleDir
	output, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || GoToolchainMismatch(err, string(output)) || GoToolOutputMissingDeps(string(output)) {
		return -1, nil, ""
	}
	idx, paths := earlierPhaseDiagnosticPaths(v, GoFilePathsFromCompileOutput(string(output), v.LayoutRoot))
	return idx, paths, string(output)
}

func earlierPhaseDiagnosticPaths(v WorkflowValidation, diagnosticPaths []string) (int, []string) {
	active := v.ActivePhaseIndex()
	first := -1
	var paths []string
	for _, path := range diagnosticPaths {
		idx := v.FindDeliveryPhaseForFile(path)
		if idx < 0 || idx >= active {
			continue
		}
		if first < 0 || idx < first {
			first = idx
			paths = nil
		}
		if idx == first {
			paths = appendUniquePath(paths, path)
		}
	}
	return first, paths
}

func rewindCompletedPhaseVerificationFailure(townRoot, rig string, v WorkflowValidation, failureOutput string) (string, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	idx, paths, output := completedPhasePythonFailure(rigDir, v, failureOutput)
	kind := "Python verification errors"
	if idx < 0 {
		idx, paths, output = completedPhaseGoCompileFailure(rigDir, v)
		kind = "compiler errors"
	}
	if idx < 0 {
		return recoverSamePhaseTestFailure(townRoot, rig, v, failureOutput)
	}
	phase := v.DeliveryPhases[idx]
	if _, err := performRewindToPhase(townRoot, rig, phase.ID, v.ActivePhaseID(), paths); err != nil {
		return "", err
	}
	reloaded, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return "", err
	}
	reopened, err := reopenClosedImplementBeadsForPaths(townRoot, rig, reloaded.ForActivePhase(), phase.RequiredFiles)
	if err != nil {
		return "", err
	}
	_ = commitDoltWorkingSet(townRoot, rig)
	logLine := fmt.Sprintf("rewound active phase %s → %s for %s in %s", v.ActivePhaseID(), phase.ID, kind, strings.Join(paths, ", "))
	if len(reopened) > 0 {
		logLine += "; reopened phase beads: " + strings.Join(reopened, ", ")
	}
	return logLine, fmt.Errorf("%s\n\nVerification output:\n%s\nRepair this phase's files against SPEC/architecture, preserving their contracts, before returning to %s", logLine, truncateWorkflowText(output, 4000), v.ActivePhaseID())
}
