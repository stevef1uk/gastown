package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// validateImplementationBeadClose runs disk/bead reconciliation before bd close:
// the closing bead's file (and correlated *_test.go) must exist, and any other closed
// beads with missing artifacts are reopened so the polecat fixes them first.
func validateImplementationBeadClose(cmd, townRoot, rig string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if !isBeadCloseCommand(cmd) {
		return nil
	}
	if !verifyOK {
		return nil
	}
	id := strings.Trim(extractBeadIDFromBdClose(cmd), `"'`)
	if id == "" {
		return nil
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, id, v)
	if beadPath != "" && strings.HasSuffix(filepath.ToSlash(beadPath), "/go.mod") {
		if logLine, repairErr := orchestrator.RepairGoModRequiresFromSpec(rigDir, v); repairErr != nil {
			return fmt.Errorf("cannot bd close %s: %w", id, repairErr)
		} else if logLine != "" {
			orchestratedPrintf("[gt-agent] %s\n", logLine)
		}
		if err := orchestrator.ValidateGoModFileForBeadClose(rigDir, v); err != nil {
			return fmt.Errorf("cannot bd close %s: %w — READ SPEC.md Module section and EDIT go.mod", id, err)
		}
	}
	if beadPath != "" && !orchestrator.IsProjectSetupArtifactPath(beadPath, v) {
		if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, beadPath, v); err != nil {
			return fmt.Errorf("cannot bd close %s: %w — implement and run Verify first", id, err)
		}
		if testPath := orchestrator.CorrelatedTestPathForSource(beadPath, v); testPath != "" {
			if orchestrator.TestPathListedInRequired(beadPath, v) {
				if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, testPath, v); err != nil {
					if orchestrator.TestPathCoveredByOtherOpenBead(townRoot, rig, id, testPath, v) {
						openID := orchestrator.OpenBeadIDForPath(townRoot, rig, testPath, v)
						orchestratedPrintf("[gt-agent] deferring test check for %s: belongs to open bead %s\n", testPath, openID)
					} else {
						return fmt.Errorf("cannot bd close %s: %w — add/pass tests before close", id, err)
					}
				}
			}
		}
	}
	reopened, err := orchestrator.ReconcileClosedImplementBeads(townRoot, rig, v)
	if err != nil {
		return fmt.Errorf("bead reconcile before bd close: %w", err)
	}
	for _, reopenedID := range reopened {
		if reopenedID != id {
			return fmt.Errorf("reopened %s (artifact missing/stub) — fix that bead before bd close %s", reopenedID, id)
		}
		return fmt.Errorf("cannot bd close %s: artifact still missing or stub on disk", id)
	}
	return nil
}
