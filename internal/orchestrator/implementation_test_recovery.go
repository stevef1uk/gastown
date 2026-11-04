package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func pendingImplementationRepairIDs(townRoot, rig string, known map[string]bool) []string {
	snapshot, err := LoadInstancesSnapshot(townRoot)
	if err != nil || snapshot == nil {
		return nil
	}
	for _, inst := range snapshot.Instances {
		if inst.Variables["rig"] != rig || inst.CurrentState != "implementation" || inst.PendingRework == nil {
			continue
		}
		rework := inst.PendingRework
		if rework.FromState == "implementation" && IsFailureOutcome(rework.Outcome) {
			return ExtractKnownRigBeadIDsFromSummary(rework.Summary, "", known)
		}
	}
	return nil
}

func samePhaseTestFailurePaths(rigDir string, v WorkflowValidation, output string) []string {
	var tests []string
	if GoCompileErrorsOnlyInTestFiles(output, v.LayoutRoot) && strings.Contains(output, "[build failed]") && !GoToolchainMismatch(nil, output) && !GoToolOutputMissingDeps(output) {
		tests = GoFilePathsFromCompileOutput(output, v.LayoutRoot)
	} else if path := pythonVerificationDiagnosticPath(rigDir, v, output); path != "" {
		if IsTestImplementPath(path) {
			tests = []string{path}
		} else {
			for _, re := range []*regexp.Regexp{pythonReworkFrameRE, pythonReworkPytestFrameRE} {
				for _, frame := range re.FindAllStringSubmatch(output, -1) {
					test := ownedPythonDiagnosticPath(rigDir, frame[1], v)
					if SourcePathForOwnedTest(test, v) == path {
						tests = appendUniquePath(tests, test)
					}
				}
			}
		}
	}
	var owned []string
	for _, test := range tests {
		if SourcePathForOwnedTest(test, v) == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(rigDir, test)); err == nil && !info.IsDir() {
			owned = appendUniquePath(owned, test)
		}
	}
	return owned
}

func recoverSamePhaseTestFailure(townRoot, rig string, v WorkflowValidation, output string) (string, error) {
	v = v.ForActivePhase()
	tests := samePhaseTestFailurePaths(filepath.Join(townRoot, rig, "mayor", "rig"), v, output)
	var owners []string
	for _, test := range tests {
		owners = appendUniquePath(owners, SourcePathForOwnedTest(test, v))
	}
	owners = OrderRequiredFilesForImplementation(owners)
	if len(owners) == 0 {
		return "", nil
	}
	for _, owner := range owners {
		if _, _, open := OpenImplementBeadForPath(townRoot, rig, owner, v); !open {
			if id, err := ReopenClosedBeadForRework(townRoot, rig, owner, v); err != nil || id == "" {
				return "", err
			}
		}
	}
	id, _, ok := OpenImplementBeadForPath(townRoot, rig, owners[0], v)
	if !ok {
		return "", nil
	}
	inProgress, err := listImplementBeadsForGuard(townRoot, rig, v, "in_progress")
	if err != nil {
		return "", err
	}
	for _, bead := range inProgress {
		if bead.ID != id {
			if err := bdUpdateImplementBeadStatus(townRoot, rig, bead.ID, "open"); err != nil {
				return "", err
			}
		}
	}
	if err := bdUpdateImplementBeadStatus(townRoot, rig, id, "in_progress"); err != nil {
		return "", err
	}
	_ = commitDoltWorkingSet(townRoot, rig)
	logLine := fmt.Sprintf("same-phase test repair assigned to %s (%s); failing companions: %s", id, owners[0], strings.Join(tests, ", "))
	return logLine, fmt.Errorf("%s\n\nVerification output:\n%s\nRepair the owning implementation and test companion against SPEC/architecture. Preserve meaningful assertions and the declared API; do not invent compatibility types or change the architecture merely to silence stale tests", logLine, truncateWorkflowText(output, 4000))
}
