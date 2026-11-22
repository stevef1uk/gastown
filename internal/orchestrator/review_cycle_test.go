package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReqFlowMatchesRigFlowAfterIntake(t *testing.T) {
	load := func(name string) WorkflowTemplate {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("town", "templates", name))
		if err != nil {
			t.Fatal(err)
		}
		var template WorkflowTemplate
		if err := yaml.Unmarshal(data, &template); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return template
	}

	rig := load("rig-flow.yaml")
	req := load("req-flow.yaml")
	if got := rig.States["plan_review"].Hooks.ReviewFailureLimit; got != 3 {
		t.Fatalf("rig-flow plan_review limit = %d, want 3", got)
	}
	if got := rig.States["qa_review"].Hooks.ReviewFailureLimit; got != 3 {
		t.Fatalf("rig-flow qa_review limit = %d, want 3", got)
	}
	if got := rig.States["qa_review"].Hooks.ReviewRecoveryState; got != "review_recovery" {
		t.Fatalf("rig-flow qa_review recovery = %q, want review_recovery", got)
	}
	for name, state := range rig.States {
		if name == "kickoff" {
			continue
		}
		reqState, ok := req.States[name]
		if !ok || !reflect.DeepEqual(reqState, state) {
			t.Errorf("req-flow state %q is missing or differs from rig-flow", name)
		}
	}
	for name := range req.States {
		if _, ok := rig.States[name]; !ok && name != "analysis" && name != "spec_review" {
			t.Errorf("unexpected req-flow-only state after intake: %q", name)
		}
	}
	if !reflect.DeepEqual(req.StuckGuard, rig.StuckGuard) ||
		!reflect.DeepEqual(req.ReworkFeedback, rig.ReworkFeedback) ||
		!reflect.DeepEqual(req.Validation, rig.Validation) {
		t.Fatal("req-flow shared configuration differs from rig-flow")
	}
	if got := req.States["spec_review"].Transitions["success"].To; got != "design" {
		t.Fatalf("spec_review success goes to %q, want design", got)
	}
}

func TestReviewFailureBudgetPersistsAcrossWorkflowCycle(t *testing.T) {
	townRoot := t.TempDir()
	tpl := &WorkflowTemplate{
		ID:           "review-cycle",
		InitialState: "plan_review",
		States: map[string]State{
			"plan_review": {
				Role: "qa",
				Hooks: StateHooks{
					ReviewFailureLimit:  3,
					ReviewRecoveryState: "review_recovery",
				},
				Transitions: map[string]Transition{
					"failure": {To: "architecture_rework"},
					"success": {To: "completed"},
				},
			},
			"architecture_rework": {
				Role:        "architect",
				Transitions: map[string]Transition{"success": {To: "design_review"}},
			},
			"design_review": {
				Role:        "qa",
				Transitions: map[string]Transition{"success": {To: "planning"}},
			},
			"planning": {
				Role:        "planner",
				Transitions: map[string]Transition{"success": {To: "plan_review"}},
			},
			"review_recovery": {
				Role: "mayor",
				Transitions: map[string]Transition{
					"planner_rework": {To: "planning"},
					"pause":          {To: "review_recovery"},
				},
			},
			"completed": {Role: "mayor"},
		},
	}

	manager := NewManager(townRoot)
	manager.LoadTemplate(tpl)
	id, err := manager.StartWorkflow(tpl.ID, map[string]string{"rig": "mockrig"})
	if err != nil {
		t.Fatal(err)
	}

	complete := func(agentID, outcome string) {
		t.Helper()
		if _, err := manager.CompleteTask(id, outcome, agentID, "plan still mismatches the contract", "repair plan.md", nil); err != nil {
			t.Fatal(err)
		}
	}
	reviewCycle := func() {
		t.Helper()
		complete("mockrig/qa", "failure")
		if manager.instances[id].CurrentState == "review_recovery" || manager.instances[id].Status == "paused" {
			return
		}
		complete("mockrig/architect", "success")
		complete("mockrig/qa", "success")
		complete("mockrig/planner", "success")
	}

	for attempt := 1; attempt <= 3; attempt++ {
		reviewCycle()
		progress := manager.instances[id]
		if attempt < 3 && progress.Status != "running" {
			t.Fatalf("paused after %d failures: %+v", attempt, progress)
		}
		if attempt == 3 && (progress.CurrentState != "review_recovery" || progress.Status != "running") {
			t.Fatalf("third repeated review failure did not escalate to Mayor recovery: %+v", progress)
		}

		// Reload after each cycle to prove the budget is persisted across
		// intermediate states and the Mayor recovery handoff.
		manager = NewManager(townRoot)
		manager.LoadTemplate(tpl)
	}
	progress := manager.instances[id]
	if progress.CurrentState != "review_recovery" || progress.ReviewFailures["plan_review:unphased"] != 0 {
		t.Fatalf("recovery state/count did not survive reload: %+v", progress)
	}
	if progress.PendingRework == nil || !strings.Contains(progress.PendingRework.Feedback, "Mayor must diagnose") {
		t.Fatal("Mayor recovery handoff did not retain actionable findings")
	}
	complete("mayor", "planner_rework")
	if got := manager.instances[id].CurrentState; got != "planning" {
		t.Fatalf("Mayor planner_rework routed to %q, want planning", got)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		complete("mockrig/planner", "success")
		complete("mockrig/qa", "failure")
		progress = manager.instances[id]
		if attempt < 3 {
			if progress.Status != "running" || progress.ReviewFailures["plan_review:unphased"] != attempt {
				t.Fatalf("review retry budget after recovery attempt %d = %+v", attempt, progress)
			}
			complete("mockrig/architect", "success")
			complete("mockrig/qa", "success")
		}
	}
	paused := manager.instances[id]
	if paused.Status != "paused" || paused.CurrentState != "review_recovery" ||
		paused.ReviewFailures["plan_review:unphased"] != 3 ||
		!strings.Contains(paused.PauseReason, "including after Mayor diagnosis/recovery") {
		t.Fatalf("review failure after Mayor recovery did not pause at diagnosis: %+v", paused)
	}
}

func TestReviewFailureBudgetIsPhaseScopedAndSuccessResets(t *testing.T) {
	inst := WorkflowInstance{}
	inst.recordReviewFailure("plan_review", "phase-1", "failure", 3, "recovery")
	inst.recordReviewFailure("plan_review", "phase-2", "failure", 3, "recovery")
	if _, ok := inst.ReviewFailures["plan_review:phase-1"]; ok {
		t.Fatal("phase change retained the previous review budget")
	}
	if got := inst.ReviewFailures["plan_review:phase-2"]; got != 1 {
		t.Fatalf("other phase budget = %d, want 1", got)
	}
	inst.recordReviewFailure("plan_review", "phase-2", "checkpoint", 3, "recovery")
	if got := inst.ReviewFailures["plan_review:phase-2"]; got != 1 {
		t.Fatalf("checkpoint changed the review budget to %d, want 1", got)
	}
	inst.recordReviewFailure("plan_review", "phase-2", "success", 3, "recovery")
	if len(inst.ReviewFailures) != 0 {
		t.Fatalf("successful review retained failure budget: %v", inst.ReviewFailures)
	}
	inst.recordReviewFailure("plan_review", "phase-1", "failure", 3, "recovery")
	if got := inst.ReviewFailures["plan_review:phase-1"]; got != 1 {
		t.Fatalf("returning to an earlier phase reused old budget: %d", got)
	}
}

func TestReviewFailureBudgetMigratesAlreadyEscalatedWorkflow(t *testing.T) {
	const key = "plan_review:phase-1-app"
	inst := WorkflowInstance{
		ReviewFailures:    map[string]int{key: 6},
		ReviewEscalations: map[string]int{key: 1},
	}

	for attempt := 1; attempt <= 3; attempt++ {
		action := inst.recordReviewFailure("plan_review", "phase-1-app", "failure", 3, "review_recovery")
		if attempt < 3 {
			if action != reviewFailureContinue || inst.ReviewFailures[key] != attempt {
				t.Fatalf("legacy recovery retry %d: action=%v failures=%d", attempt, action, inst.ReviewFailures[key])
			}
			continue
		}
		if action != reviewFailurePause || inst.ReviewFailures[key] != 3 {
			t.Fatalf("legacy recovery budget did not pause after three fresh failures: action=%v failures=%d", action, inst.ReviewFailures[key])
		}
	}

	if action := inst.recordReviewFailure("plan_review", "phase-1-app", "failure", 3, "review_recovery"); action != reviewFailurePause {
		t.Fatalf("exhausted migrated budget was reset a second time: action=%v", action)
	}
}

func TestResetWorkflowClearsReviewFailureBudget(t *testing.T) {
	tpl := &WorkflowTemplate{
		ID:           "review-reset",
		InitialState: "plan_review",
		States: map[string]State{
			"plan_review": {Role: "qa", Transitions: map[string]Transition{"success": {To: "completed"}}},
			"design":      {Role: "architect"},
			"completed":   {Role: "mayor"},
		},
	}
	manager, id := loadTestManager(t, tpl)
	manager.instances[id].ReviewFailures = map[string]int{"plan_review:phase-1": 3}
	manager.instances[id].ReviewEscalations = map[string]int{"plan_review:phase-1": 1}
	manager.instances[id].ReviewRecoveryBudgetStarted = map[string]bool{"plan_review:phase-1": true}
	manager.instances[id].Status = "paused"

	if _, err := manager.ResetWorkflow(id, "plan_review"); err != nil {
		t.Fatal(err)
	}
	if manager.instances[id].ReviewFailures != nil || manager.instances[id].ReviewEscalations != nil || manager.instances[id].ReviewRecoveryBudgetStarted != nil {
		t.Fatalf("manual workflow reset retained review budget: failures=%v escalations=%v recovery_started=%v", manager.instances[id].ReviewFailures, manager.instances[id].ReviewEscalations, manager.instances[id].ReviewRecoveryBudgetStarted)
	}
}
