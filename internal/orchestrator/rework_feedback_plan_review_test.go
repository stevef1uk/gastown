package orchestrator

import "testing"

func TestPlanReviewFailureNeedsArchitect(t *testing.T) {
	t.Parallel()
	summary := "architecture.md Store API signatures drift from SPEC: Create takes (db *sql.DB)"
	if !PlanReviewFailureNeedsArchitect(summary) {
		t.Fatal("expected architect escalation")
	}
	if PlanReviewFailureNeedsArchitect("duplicate te-abc beads for main.go") {
		t.Fatal("bead-only failures should stay with planner")
	}
}
