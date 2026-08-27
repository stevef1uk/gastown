package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsPlanningGateState reports FSM states that require artifact alignment before success transitions.
func IsPlanningGateState(fromState string) bool {
	switch strings.TrimSpace(fromState) {
	case "design", "planning", "plan_review", "project_setup":
		return true
	default:
		return false
	}
}

// IsPlanningGateSuccessOutcome reports outcomes that advance past planning gates (orchestrator complete_task).
func IsPlanningGateSuccessOutcome(outcome string) bool {
	return strings.EqualFold(strings.TrimSpace(outcome), "success")
}

// ValidatePlanningPhaseGate enforces SPEC/architecture/plan/bead alignment before the FSM leaves
// design, planning, plan_review, or project_setup on success. Called from Manager.CompleteTask so
// MCP complete_task and manual workflow complete cannot bypass gt-agent validators.
func ValidatePlanningPhaseGate(townRoot, rig, fromState string, v WorkflowValidation) error {
	if townRoot == "" || rig == "" || !IsPlanningGateState(fromState) {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	v = v.ForActivePhase()

	switch fromState {
	case "design":
		if err := ValidateArchitectureDocAlignment(rigDir, v); err != nil {
			return fmt.Errorf("design success blocked: %w", err)
		}
		return nil
	case "planning", "plan_review":
		if err := validatePlanningGateArtifacts(townRoot, rig, rigDir, fromState, v); err != nil {
			return fmt.Errorf("%s success blocked: %w", fromState, err)
		}
		return nil
	case "project_setup":
		// project_setup only verifies the toolchain (go mod tidy, go test, etc.).
		// Plan.md alignment was already validated during planning/plan_review —
		// re-validating here causes false-positive path-normalization loops that
		// block the entire workflow for no benefit.
		return nil
	default:
		return nil
	}
}

func validatePlanningGateArtifacts(townRoot, rig, rigDir, fromState string, v WorkflowValidation) error {
	planPath := filepath.Join(rigDir, "plan.md")
	info, err := os.Stat(planPath)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", planPath)
	}
	minPlan := EffectiveMinPlanBytes(rigDir, v)
	if info.Size() < minPlan {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d (%s)", info.Size(), minPlan, v.PlanMinSizeHint())
	}
	specDoc := readRigDoc(rigDir, "SPEC.md")
	archDoc := readRigDoc(rigDir, "architecture.md")
	if strings.TrimSpace(specDoc) != "" && strings.TrimSpace(archDoc) != "" {
		if issues := architectureDocAlignmentIssuesForDoc(archDoc, specDoc, v, false); len(issues) > 0 {
			return formatDocAlignmentError("SPEC/architecture misaligned", issues)
		}
	}
	if err := ValidatePlanningDocAlignment(townRoot, rigDir, v, fromState); err != nil {
		return err
	}
	if err := ValidatePlanMDBeadPathAlignment(townRoot, rig, v); err != nil {
		return err
	}
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	open, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return fmt.Errorf("list implement beads: %w", err)
	}
	archPath := filepath.Join(rigDir, "architecture.md")
	// Bead coverage is a planning concern; project_setup only prepares the
	// toolchain. Requiring open beads here prevents recovery when a phase was
	// already implemented and its beads were closed.
	if fromState != "project_setup" {
		if err := ValidatePlanBeads(open, archPath, v, rig); err != nil {
			return err
		}
		if err := ValidatePlanBeadPathsExact(open, v, rig); err != nil {
			return err
		}
	}
	if fromState == "project_setup" && WorkflowUsesGo(v) {
		goMod := ResolveRequiredFileOnDisk(rigDir, "go.mod", v.LayoutRoot)
		if _, err := os.Stat(goMod); err != nil {
			return fmt.Errorf("go.mod missing at %s after project_setup", goMod)
		}
	}
	return nil
}
