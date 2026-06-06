package main

import "github.com/steveyegge/gastown/internal/orchestrator"

func (r *stateRunner) rejectPlanReviewSpuriousFailure(outcome, summary string) (string, bool) {
	if r == nil || r.hooks.Artifacts != "plan_review" || !isOrchestratedFailureOutcome(outcome) {
		return "", false
	}
	if reason := orchestrator.PlanReviewSpuriousFailureReason(r.townRoot, r.rig, summary, r.v); reason != "" {
		return reason + " Reply with JSON only after re-checking bd list and plan.md.", true
	}
	return "", false
}

func (r *stateRunner) tryPlanReviewFailureToSuccess(outcome string) (string, string, bool) {
	if r == nil || r.hooks.Artifacts != "plan_review" || !isOrchestratedFailureOutcome(outcome) {
		return "", "", false
	}
	if err := r.validateArtifacts("success"); err != nil {
		return "", "", false
	}
	return "success", "Open/in_progress beads and plan.md satisfy plan_review (mechanical gate passed)", true
}
