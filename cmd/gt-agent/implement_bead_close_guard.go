package main

import (
	"fmt"
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
	if beadPath != "" && !orchestrator.IsProjectSetupArtifactPath(beadPath, v) {
		if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, beadPath, v); err != nil {
			return fmt.Errorf("cannot bd close %s: %w — implement and run Verify first", id, err)
		}
		if testPath := orchestrator.CorrelatedTestPathForSource(beadPath, v.LayoutRoot); testPath != "" {
			// Separate *_test.go implement bead (e.g. handlers_test.go) — do not require test file when closing handlers.go.
			if !orchestrator.TestPathListedInRequired(beadPath, v.RequiredFiles, v.LayoutRoot) {
				if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, testPath, v); err != nil {
					return fmt.Errorf("cannot bd close %s: %w — add/pass tests before close", id, err)
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
