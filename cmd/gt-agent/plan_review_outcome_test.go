package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestPlanningReworkCannotPassUnchangedPlan(t *testing.T) {
	town, rig := t.TempDir(), "mockrig"
	rigDir := rigMayorRigDir(town, rig)
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(rigDir, "plan.md")
	if err := os.WriteFile(path, []byte("# Plan\nGeneric acceptance\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	r := &stateRunner{
		townRoot: town, rig: rig,
		initialPlanContent: []byte("# Plan\nGeneric acceptance\n"),
		task:               &orchestrator.Task{AllowedOutcomes: []string{"success", "failure"}, PendingRework: &orchestrator.WorkflowRework{FromState: "plan_review", Summary: "Expand behavioral acceptance"}},
		hooks:              orchestrator.StateHooks{Artifacts: "planning"},
		track:              &cmdTracker{startedAt: time.Now()},
	}
	if err := r.validateArtifacts("success"); err == nil || !strings.Contains(err.Error(), "amend plan.md") {
		t.Fatalf("unchanged plan passed rework: %v", err)
	}
	if outcome, _, ok := r.tryAutoOutcome(); ok {
		t.Fatalf("unchanged plan auto-completed rework with %q", outcome)
	}
	if err := os.WriteFile(path, r.initialPlanContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.validatePlanningRework(); err == nil {
		t.Fatal("regenerating identical plan content must not count as rework progress")
	}
	if err := os.WriteFile(path, []byte("# Plan\nAcceptance: add, reload, and delete a link through the UI.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.validatePlanningRework(); err != nil {
		t.Fatalf("amended plan must proceed to the normal artifact checks: %v", err)
	}
	r.task.PendingRework = nil
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := r.validatePlanningRework(); err != nil {
		t.Fatalf("initial planning can use a synchronized plan: %v", err)
	}
}

func TestPlanningAutoCompleteSkipsDependencyCycle(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := rigMayorRigDir(town, rig)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := "### sm-a: app/a.txt\n- Depends on: app/b.txt\n### sm-b: app/b.txt\n- Depends on: app/a.txt\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{ActivePhaseIDField: "one", DeliveryPhases: []orchestrator.DeliveryPhase{{
		ID: "one", RequiredFiles: []string{"app/a.txt", "app/b.txt"},
	}}}
	if !shouldSkipPlanningAutoComplete(&orchestrator.Task{State: "planning"}, town, rig, v) {
		t.Fatal("planning auto-completed a plan with a dependency cycle")
	}
	if err := validatePlanningArtifacts(town, rig, false, false, false, v); err == nil || !strings.Contains(err.Error(), "file dependency cycle") || strings.Contains(err.Error(), "bd create") {
		t.Fatalf("planning did not report the actionable dependency defect: %v", err)
	}
}
