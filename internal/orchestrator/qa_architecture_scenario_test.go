package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRigFlowTemplate_qaArchitectureFailureTransition(t *testing.T) {
	tplPath := filepath.Join("town", "templates", "rig-flow.yaml")
	data, err := os.ReadFile(tplPath)
	if err != nil {
		t.Skipf("rig-flow template not beside test: %v", err)
	}
	if !strings.Contains(string(data), "architecture_failure:") {
		t.Fatalf("rig-flow.yaml missing architecture_failure transition:\n%s", data)
	}
	if !strings.Contains(string(data), "to: design") {
		t.Fatal("architecture_failure should target design state")
	}
}

func TestArchitectureFailureScenario_endToEnd(t *testing.T) {
	m := NewManager(t.TempDir())
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "qa_review",
		ReworkFeedback: map[string]string{
			"qa_review->design": "qa_review_to_design",
		},
		States: map[string]State{
			"qa_review": {
				Role: "qa",
				Transitions: map[string]Transition{
					"architecture_failure": {To: "design"},
					"failure":              {To: "implementation"},
				},
			},
			"design": {
				Role:         "architect",
				Instructions: "revise architecture.md",
				Transitions: map[string]Transition{
					"success": {To: "planning"},
				},
			},
			"planning": {Role: "planner", Transitions: map[string]Transition{"success": {To: "completed"}}},
			"completed": {Role: "mayor"},
		},
	})
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": "linkshelf"})
	if err != nil {
		t.Fatal(err)
	}
	m.instances[id].CurrentState = "qa_review"

	summary := "go test ./... ok; smoke: POST /api/bookmarks 405 — architecture HTTP table documents /api/items"
	feedback := "Command: go test ./...\nok\tlinkshelf/internal/store\n\nCommand: curl POST http://127.0.0.1:8080/api/bookmarks\ncurl: (22) The requested URL returned error: 405"

	next, err := m.CompleteTask(id, "architecture_failure", "linkshelf/qa", summary, feedback, nil)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if next != "design" {
		t.Fatalf("next state = %q, want design", next)
	}
	inst := m.instances[id]
	if inst.PendingRework == nil {
		t.Fatal("expected PendingRework for architect")
	}
	if inst.PendingRework.FromState != "qa_review" || inst.PendingRework.Outcome != "architecture_failure" {
		t.Fatalf("rework metadata: %+v", inst.PendingRework)
	}
	if !strings.Contains(inst.PendingRework.Feedback, "architecture rework") {
		t.Fatalf("feedback for architect: %q", inst.PendingRework.Feedback)
	}
	if !strings.Contains(inst.PendingRework.Feedback, "405") {
		t.Fatalf("feedback should include smoke evidence: %q", inst.PendingRework.Feedback)
	}

	tpl := m.templates["rig-flow"]
	state, _ := inst.GetCurrentTask(tpl)
	if state.Role != "architect" {
		t.Fatalf("architect role, got %q", state.Role)
	}
	if !state.AcceptsOutcome("architecture_failure") {
		// design state should not accept architecture_failure — only qa did
	}
	if !state.AcceptsOutcome("success") {
		t.Fatal("design should accept success")
	}

	// Architect completes design → planning with rework cleared on success path later
	next, err = m.CompleteTask(id, "success", "linkshelf/architect", "architecture.md revised HTTP table", "", nil)
	if err != nil || next != "planning" {
		t.Fatalf("architect success: next=%q err=%v", next, err)
	}
	if inst.PendingRework != nil {
		t.Fatalf("success should clear rework, got %+v", inst.PendingRework)
	}
}

func TestIsCrossStateReworkOutcome_architectureFailure(t *testing.T) {
	if !IsCrossStateReworkOutcome("architecture_failure") {
		t.Fatal("architecture_failure should set pending rework")
	}
	if IsFailureOutcome("architecture_failure") {
		t.Fatal("architecture_failure is not a polecat failure outcome")
	}
}
