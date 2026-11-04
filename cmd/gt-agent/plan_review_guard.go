package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func (r *stateRunner) validatePlanningRework() error {
	if r.task == nil || r.task.PendingRework == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(rigMayorRigDir(r.townRoot, r.rig), "plan.md"))
	if err != nil {
		return fmt.Errorf("read plan.md for planning rework: %w", err)
	}
	if bytes.Equal(data, r.initialPlanContent) {
		return fmt.Errorf("planning rework is pending: amend plan.md to address the review feedback before reporting success; listing beads or checking byte count is not a repair")
	}
	return nil
}

func (r *stateRunner) rejectPlanReviewSpuriousFailure(outcome, summary string) (string, bool) {
	if r == nil || r.hooks.RequireExplicitOutcome || r.hooks.Artifacts != "plan_review" || !isOrchestratedFailureOutcome(outcome) {
		return "", false
	}
	if reason := orchestrator.PlanReviewSpuriousFailureReason(r.townRoot, r.rig, summary, r.v); reason != "" {
		return reason + " Reply with JSON only after re-checking bd list and plan.md.", true
	}
	return "", false
}

func (r *stateRunner) tryPlanReviewFailureToSuccess(outcome string) (string, string, bool) {
	if r == nil || r.hooks.RequireExplicitOutcome || r.hooks.Artifacts != "plan_review" || !isOrchestratedFailureOutcome(outcome) {
		return "", "", false
	}
	if err := r.validateArtifacts("success"); err != nil {
		return "", "", false
	}
	return "success", "Open/in_progress beads and plan.md satisfy plan_review (mechanical gate passed)", true
}
