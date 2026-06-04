package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WorkflowStuckRepairLog summarizes deterministic repair steps applied to a stuck rig.
type WorkflowStuckRepairLog struct {
	Rig     string
	Signals []WorkflowStuckSignal
	Steps   []string
}

// RunWorkflowStuckRepair runs idempotent corrective actions in a fixed order.
func RunWorkflowStuckRepair(townRoot, rig string, v WorkflowValidation, signals []WorkflowStuckSignal) (*WorkflowStuckRepairLog, error) {
	if townRoot == "" || rig == "" {
		return nil, nil
	}
	v = ValidationForPlanningSync(townRoot, rig, v.ForActivePhase())
	log := &WorkflowStuckRepairLog{Rig: rig, Signals: signals}
	appendStep := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			log.Steps = append(log.Steps, s)
		}
	}

	rigDir := RigMayorDir(townRoot, rig)
	beadsReady := BeadsDatabaseReady(townRoot, rig)

	if beadsReady {
		if line, err := EnsureRigBeadOperationalForWorkflow(townRoot, rig); err != nil {
			return log, err
		} else {
			appendStep(line)
		}
	}

	if beadsReady {
		forcePlan := RequiresExactImplementPaths(v) || containsSignal(signals, SignalMissingIntegrationContract)
		if syncLog, err := SyncPlanningArtifacts(townRoot, rig, v, forcePlan); err != nil {
			return log, fmt.Errorf("sync planning: %w", err)
		} else {
			appendStep(syncLog)
		}

		if repairLog, err := RepairPlanningBeadSet(townRoot, rig, v); err != nil {
			return log, fmt.Errorf("repair planning beads: %w", err)
		} else {
			appendStep(repairLog)
		}

		if containsSignal(signals, SignalNonRequiredImplementBeads) {
			if pruned, err := PruneNonRequiredOpenImplementBeads(townRoot, rig, v); err != nil {
				return log, fmt.Errorf("prune non-required beads: %w", err)
			} else if len(pruned) > 0 {
				appendStep("pruned non-required: " + joinStrings(pruned, ", "))
			}
		}

		if containsSignal(signals, SignalPhaseIdleNoBeadProgress) || containsSignal(signals, SignalPendingReworkLinger) {
			if _, err := EnforceSingleImplementInProgress(townRoot, rig, v); err != nil {
				return log, fmt.Errorf("implement queue: %w", err)
			} else {
				appendStep("enforced single in_progress implement bead")
			}
		}
	}

	if containsSignal(signals, SignalMissingIntegrationContract) {
		if patched, err := ensurePlanIntegrationContract(rigDir, v); err != nil {
			return log, fmt.Errorf("integration contract: %w", err)
		} else if patched {
			appendStep("patched plan.md integration contract")
		}
	}

	return log, nil
}

func containsSignal(signals []WorkflowStuckSignal, want WorkflowStuckSignal) bool {
	for _, s := range signals {
		if s == want {
			return true
		}
	}
	return false
}

// RigMayorDir returns {town}/{rig}/mayor/rig.
func RigMayorDir(townRoot, rig string) string {
	return filepath.Join(townRoot, rig, "mayor", "rig")
}
