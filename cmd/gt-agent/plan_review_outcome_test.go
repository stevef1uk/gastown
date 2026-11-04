package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestPlanReviewRequiresExplicitVerdict(t *testing.T) {
	hooks, err := orchestrator.RigFlowStateHooks("plan_review")
	if err != nil {
		t.Fatal(err)
	}
	validate := artifactValidators["plan_review"]
	autoComplete := artifactAutoCompleters["plan_review"]
	t.Cleanup(func() {
		artifactValidators["plan_review"] = validate
		artifactAutoCompleters["plan_review"] = autoComplete
	})
	artifactValidators["plan_review"] = func(*stateRunner, string) error { return nil }
	artifactAutoCompleters["plan_review"] = func(*stateRunner) error { return nil }
	r := &stateRunner{
		task:  &orchestrator.Task{AllowedOutcomes: []string{"success", "failure"}},
		hooks: hooks,
		track: &cmdTracker{},
	}
	if outcome, _, ok := r.tryAutoOutcome(); ok {
		t.Errorf("mechanically valid artifacts auto-completed review with %q", outcome)
	}
	if outcome, _, ok := r.tryPlanReviewFailureToSuccess("failure"); ok {
		t.Errorf("explicit QA failure was converted to %q", outcome)
	}
}
