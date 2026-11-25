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

func extractGoTestDiagnosticPaths(output string, v WorkflowValidation) []string {
	var paths []string
	if strings.TrimSpace(output) == "" {
		return nil
	}
	for _, p := range GoFilePathsFromCompileOutput(output, v.LayoutRoot) {
		paths = appendUniquePath(paths, p)
	}
	for _, p := range GoTestFailureProductionPaths(output, v.LayoutRoot) {
		paths = appendUniquePath(paths, p)
	}
	active := v.ActivePhaseIndex()
	for _, m := range goTestFailLineRE.FindAllStringSubmatch(output, -1) {
		if len(m) >= 2 {
			testPath := resolveGoTestFilePath(m[1], v.LayoutRoot, output)
			if testPath != "" {
				paths = appendUniquePath(paths, testPath)
				if src := SourcePathForCorrelatedTest(testPath, v.LayoutRoot); src != "" {
					paths = appendUniquePath(paths, src)
				}
				// Only consider other package files if the failing test itself belongs to an earlier phase.
				// If the failing test belongs to the active or a future phase, it is active-phase work
				// and must NOT falsely implicate earlier-phase files in the same package.
				testPhase := v.FindDeliveryPhaseForFile(testPath)
				if testPhase >= 0 && testPhase < active {
					for _, req := range v.UnionRequiredFiles() {
						if strings.HasSuffix(req, ".go") && !IsTestImplementPath(req) && SameImplementGoPackage(testPath, req, v.LayoutRoot) {
							paths = appendUniquePath(paths, req)
						}
					}
				}
			}
		}
	}
	return paths
}

func hasGoTestFailureMarker(output string) bool {
	return strings.Contains(output, "--- FAIL:") ||
		strings.Contains(output, "FAIL\t") ||
		strings.Contains(output, "FAIL\n") ||
		strings.Contains(output, "[build failed]") ||
		strings.Contains(output, "[setup failed]") ||
		strings.Contains(output, "FAIL:")
}

func completedPhaseGoTestFailure(rigDir string, v WorkflowValidation, failureOutput string) (int, []string, string) {
	active := v.ActivePhaseIndex()
	if !WorkflowUsesGo(v) || !v.HasPhasedDelivery() || active <= 0 {
		return -1, nil, ""
	}
	moduleDir := filepath.Join(rigDir, v.LayoutRootDir())
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return -1, nil, ""
	}

	// First: execute go test ./... to check actual test status in the module.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = moduleDir
	outputBytes, err := cmd.CombinedOutput()
	if err == nil {
		// All tests pass in the workspace! Never rewind a completed phase when tests pass.
		return -1, nil, ""
	}

	// Tests failed. Check if an earlier phase's files are responsible.
	output := string(outputBytes)
	diagnosticPaths := extractGoTestDiagnosticPaths(output, v)
	if idx, paths := earlierPhaseDiagnosticPaths(v, diagnosticPaths); idx >= 0 {
		return idx, paths, output
	}

	// If go test failed but didn't identify earlier files, check failureOutput only
	// if it contains genuine Go test failure markers.
	if strings.TrimSpace(failureOutput) != "" && hasGoTestFailureMarker(failureOutput) {
		diagPaths := extractGoTestDiagnosticPaths(failureOutput, v)
		if idx, paths := earlierPhaseDiagnosticPaths(v, diagPaths); idx >= 0 {
			return idx, paths, failureOutput
		}
	}

	return -1, nil, ""
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
		idx, paths, output = completedPhaseGoTestFailure(rigDir, v, failureOutput)
		kind = "test failures"
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
