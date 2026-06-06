package orchestrator

import "strings"

// PlanReviewFailureNeedsArchitect reports plan_review failures that require architecture.md edits
// (planner cannot write architecture.md — send workflow to design instead of planning loop).
// Generic checklist wording ("architecture.md and plan.md must agree on store API names") must not
// escalate; only explicit architecture revision signals do.
func PlanReviewFailureNeedsArchitect(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	if planReviewFailureIsPlannerOwned(lower) {
		return false
	}
	if strings.Contains(lower, "revise architecture") ||
		strings.Contains(lower, "rewrite architecture") ||
		strings.Contains(lower, "architecture.md must") ||
		strings.Contains(lower, "architecture drift") ||
		strings.Contains(lower, "architecture_failure") {
		return true
	}
	if !strings.Contains(lower, "architecture.md") {
		return false
	}
	return strings.Contains(lower, "store api signature") ||
		strings.Contains(lower, "signatures drift") ||
		strings.Contains(lower, "context.context") ||
		strings.Contains(lower, "package-level") ||
		(strings.Contains(lower, "drift") && strings.Contains(lower, "spec"))
}

func planReviewFailureIsPlannerOwned(lower string) bool {
	plannerOwned := []string{
		"planner must",
		"missing bead",
		"duplicate",
		"bd delete",
		"bd create",
		"zero open implement",
		"no open implement",
		"no implement bead",
		"integration contract",
		"plan.md is missing",
		"plan.md missing",
		"plan.md does not",
		"plan.md incorrectly",
		"beads database not initialized",
		"bd list fails",
		"bd list command failed",
	}
	for _, needle := range plannerOwned {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// PlanReviewSpuriousFailureReason returns guidance when a plan_review failure summary cites
// issues that contradict mechanical checks (empty string when not spurious).
func PlanReviewSpuriousFailureReason(townRoot, rig, summary string, v WorkflowValidation) string {
	v = v.ForActivePhase()
	lower := strings.ToLower(summary)
	if !profileHasServerEntrypoint(v) &&
		strings.Contains(lower, "integration contract") {
		return "Integration contract is not required in the active phase (no cmd/.../main.go in required_files). Do not fail for a missing ## Integration contract."
	}
	if planReviewSummaryClaimsMissingImplementBead(lower) {
		covered, err := allRequiredPathsCoveredByActiveBeads(townRoot, rig, v)
		if err == nil && covered {
			return "Implement beads exist for every active-phase required_files path (open or in_progress). Re-run `bd list --status=open,in_progress` — do not fail for a missing bead."
		}
	}
	return ""
}

func planReviewSummaryClaimsMissingImplementBead(lower string) bool {
	return strings.Contains(lower, "missing bead") ||
		strings.Contains(lower, "zero open") ||
		strings.Contains(lower, "no open implement") ||
		strings.Contains(lower, "no implement bead")
}

func allRequiredPathsCoveredByActiveBeads(townRoot, rig string, v WorkflowValidation) (bool, error) {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return true, nil
	}
	for _, want := range v.RequiredFiles {
		ok, err := openBeadCoversRequiredPath(townRoot, rig, want, v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// PlanReviewGateSatisfied reports whether plan_review success preconditions hold on disk.
func PlanReviewGateSatisfied(townRoot, rig string, v WorkflowValidation) error {
	return ValidatePlanningPhaseGate(townRoot, rig, "plan_review", v.ForActivePhase())
}
