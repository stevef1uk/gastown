package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestManager(t *testing.T, tpl *WorkflowTemplate) (*Manager, string) {
	t.Helper()
	m := NewManager(t.TempDir())
	m.LoadTemplate(tpl)
	id, err := m.StartWorkflow(tpl.ID, map[string]string{"rig": "mockrig"})
	if err != nil {
		t.Fatal(err)
	}
	return m, id
}

func designFlowTemplate() *WorkflowTemplate {
	return &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "design",
		States: map[string]State{
			"design": {
				Role:         "architect",
				Instructions: "design step",
				Transitions: map[string]Transition{
					"success": {To: "planning"},
					"failure": {To: "design"},
					"fail":    {To: "design"},
				},
			},
			"planning": {
				Role:         "planner",
				Instructions: "plan step",
				Transitions: map[string]Transition{
					"success": {To: "completed"},
				},
			},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	}
}

func TestAgentMatchesTask(t *testing.T) {
	vars := map[string]string{"rig": "mockrig"}
	if !AgentMatchesTask("mockrig/architect", "architect", vars) {
		t.Fatal("rig-qualified agent should match")
	}
	if AgentMatchesTask("architect", "architect", vars) {
		t.Fatal("bare architect should not match when workflow has rig")
	}
	if !AgentMatchesTask("mayor", "mayor", vars) {
		t.Fatal("town mayor should match when workflow has rig")
	}
	if !AgentMatchesTask("mockrig/planner", "planner", vars) {
		t.Fatal("rig planner should match when workflow has rig")
	}
	if AgentMatchesTask("mockrig/planner", "architect", vars) {
		t.Fatal("wrong role should not match")
	}
}

func TestFetchTaskWithPromptFile(t *testing.T) {
	dir := t.TempDir()
	orchDir := filepath.Join(dir, "orchestrator")
	tmplDir := filepath.Join(orchDir, "templates")
	promptDir := filepath.Join(orchDir, "prompts", "rig-flow")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "kickoff.md"), []byte("# Kickoff for {{rig}}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tpl := `id: test-flow
initial_state: kickoff
states:
  kickoff:
    role: mayor
    prompt_file: prompts/rig-flow/kickoff.md
    instructions: "Verify {{rig}}"
    transitions:
      success:
        to: done
  done:
    role: mayor
    instructions: "done"
    transitions: {}
`
	if err := os.WriteFile(filepath.Join(tmplDir, "test-flow.yaml"), []byte(tpl), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	if err := m.LoadTemplatesFromDir(tmplDir); err != nil {
		t.Fatal(err)
	}
	wf, err := m.StartWorkflow("test-flow", map[string]string{"rig": "myrig"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.FetchTask("mayor")
	if err != nil {
		t.Fatal(err)
	}
	if task["workflow_id"] != wf {
		t.Fatalf("workflow_id: %v", task["workflow_id"])
	}
	if task["template_id"] != "test-flow" {
		t.Fatalf("template_id: %v", task["template_id"])
	}
	sp := task["system_prompt"].(string)
	if sp == "" || !strings.Contains(sp, "myrig") {
		t.Fatalf("system_prompt: %q", sp)
	}
}

func TestSkipsCompletedInstances(t *testing.T) {
	m := NewManager(t.TempDir())
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "x",
		InitialState: "a",
		States: map[string]State{
			"a": {Role: "mayor", Instructions: "go", Transitions: map[string]Transition{
				"success": {To: "completed"},
			}},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	})
	id, _ := m.StartWorkflow("x", nil)
	m.CompleteTask(id, "success", "mayor", "", "")
	_, err := m.FetchTask("mayor")
	if err == nil {
		t.Fatal("expected no task for completed workflow")
	}
}

func TestSkipsFailedInstances(t *testing.T) {
	m := NewManager(t.TempDir())
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "x",
		InitialState: "a",
		States: map[string]State{
			"a": {Role: "mayor", Instructions: "go", Transitions: map[string]Transition{
				"success": {To: "failed"},
			}},
			"failed": {Role: "mayor", Instructions: "dead"},
		},
	})
	id, _ := m.StartWorkflow("x", nil)
	m.instances[id].Status = "failed"
	_, err := m.FetchTask("mayor")
	if err == nil {
		t.Fatal("expected no task for failed workflow")
	}
}

func TestCompleteTask_validTransitionAndTerminal(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	next, err := m.CompleteTask(id, "success", "mockrig/architect", "", "")
	if err != nil || next != "planning" {
		t.Fatalf("CompleteTask success: next=%q err=%v", next, err)
	}
	if m.instances[id].CurrentState != "planning" {
		t.Fatalf("state: %q", m.instances[id].CurrentState)
	}

	next, err = m.CompleteTask(id, "success", "mockrig/planner", "", "")
	if err != nil || next != "completed" {
		t.Fatalf("CompleteTask to terminal: next=%q err=%v", next, err)
	}
	if m.instances[id].Status != "completed" {
		t.Fatalf("status: %q", m.instances[id].Status)
	}
	_, err = m.FetchTask("mayor")
	if err == nil {
		t.Fatal("terminal workflow should not fetch tasks")
	}
}

func TestCompleteTask_failAlias(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	next, err := m.CompleteTask(id, "fail", "mockrig/architect", "", "")
	if err != nil || next != "design" {
		t.Fatalf("fail alias: next=%q err=%v", next, err)
	}
}

func TestCompleteTask_rejectsWrongAgent(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	_, err := m.CompleteTask(id, "success", "mayor", "", "")
	if err == nil || !strings.Contains(err.Error(), "cannot complete") {
		t.Fatalf("mayor must not complete design (architect role): %v", err)
	}
}

func TestCompleteTask_invalidOutcome(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	_, err := m.CompleteTask(id, "bogus", "mockrig/architect", "", "")
	if err == nil {
		t.Fatal("expected error for invalid outcome")
	}
	if m.instances[id].CurrentState != "design" {
		t.Fatalf("state should be unchanged, got %q", m.instances[id].CurrentState)
	}
}

func TestGetWorkflowStatus(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	all, err := m.GetWorkflowStatus("")
	if err != nil || len(all) != 1 {
		t.Fatalf("all statuses: len=%d err=%v", len(all), err)
	}
	if all[0].Role != "architect" || all[0].TemplateID != "rig-flow" {
		t.Fatalf("snapshot: %+v", all[0])
	}

	one, err := m.GetWorkflowStatus(id)
	if err != nil || len(one) != 1 || one[0].ID != id {
		t.Fatalf("by id: %+v err=%v", one, err)
	}

	_, err = m.GetWorkflowStatus("wf-missing")
	if err == nil {
		t.Fatal("expected error for missing workflow id")
	}
}

func TestResetWorkflow_rewindsToDesign(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())
	_, _ = m.CompleteTask(id, "success", "mockrig/architect", "", "")
	if m.instances[id].CurrentState != "planning" {
		t.Fatalf("want planning, got %s", m.instances[id].CurrentState)
	}
	next, err := m.ResetWorkflow(id, "design")
	if err != nil || next != "design" {
		t.Fatalf("ResetWorkflow: next=%q err=%v", next, err)
	}
	if m.instances[id].CurrentState != "design" || m.instances[id].Status != "running" {
		t.Fatalf("after reset: state=%s status=%s", m.instances[id].CurrentState, m.instances[id].Status)
	}
}

func TestStartWorkflow_rejectsDuplicateActive(t *testing.T) {
	m, _ := loadTestManager(t, designFlowTemplate())
	_, err := m.StartWorkflow("rig-flow", map[string]string{"rig": "mockrig"})
	if err != ErrWorkflowAlreadyActive {
		t.Fatalf("want ErrWorkflowAlreadyActive, got %v", err)
	}
}

func TestHasActiveWorkflow(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	if !m.HasActiveWorkflow("rig-flow", "mockrig") {
		t.Fatal("expected active rig-flow for mockrig")
	}
	if m.HasActiveWorkflow("rig-flow", "other") {
		t.Fatal("should not match other rig")
	}
	if m.HasActiveWorkflow("other-tpl", "mockrig") {
		t.Fatal("should not match other template")
	}

	_, _ = m.CompleteTask(id, "success", "mockrig/architect", "", "")
	_, _ = m.CompleteTask(id, "success", "mockrig/planner", "", "")
	if m.HasActiveWorkflow("rig-flow", "mockrig") {
		t.Fatal("completed workflow should not be active")
	}
}

func TestValidateTemplateSchema(t *testing.T) {
	warn := validateTemplateSchema(&WorkflowTemplate{
		ID: "bad",
		States: map[string]State{
			"intake": {PromptFile: "intake.md", Instructions: "no role field"},
		},
	}, "idea.yaml")
	if warn == "" || !strings.Contains(warn, "missing role") || !strings.Contains(warn, "agent_role") {
		t.Fatalf("expected schema warning, got %q", warn)
	}
	if validateTemplateSchema(&WorkflowTemplate{
		ID: "ok",
		States: map[string]State{
			"start": {Role: "mayor"},
		},
	}, "ok.yaml") != "" {
		t.Fatal("valid template should not warn")
	}
}

func TestLoadTemplatesFromDir_warnsOnAgentRoleOnlyYAML(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "orchestrator", "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `id: agent-role-only
initial_state: intake
states:
  intake:
    agent_role: architect
    instructions: "draft"
    transitions:
      success:
        to: prd-review
  prd-review:
    agent_role: reviewer
    instructions: "review"
    transitions: {}
`
	if err := os.WriteFile(filepath.Join(tmplDir, "agent-role-only.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	if err := m.LoadTemplatesFromDir(tmplDir); err != nil {
		t.Fatal(err)
	}
	tpl := m.templates["agent-role-only"]
	if tpl == nil {
		t.Fatal("template should still load")
	}
	if tpl.States["intake"].Role != "" {
		t.Fatalf("agent_role should not populate role field, got %q", tpl.States["intake"].Role)
	}
	id, err := m.StartWorkflow("agent-role-only", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.FetchTask("architect")
	if !errors.Is(err, ErrNoTask) {
		t.Fatalf("empty role state should not match architect: %v", err)
	}
	_ = id
}

func TestFetchTask_prefersActiveOverCompleted(t *testing.T) {
	m := NewManager(t.TempDir())
	tpl := &WorkflowTemplate{
		ID:           "x",
		InitialState: "a",
		States: map[string]State{
			"a": {Role: "mayor", Instructions: "go", Transitions: map[string]Transition{
				"success": {To: "completed"},
			}},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	}
	m.LoadTemplate(tpl)
	doneID, _ := m.StartWorkflow("x", nil)
	m.CompleteTask(doneID, "success", "mayor", "", "")
	activeID, _ := m.StartWorkflow("x", nil)
	task, err := m.FetchTask("mayor")
	if err != nil {
		t.Fatal(err)
	}
	if task["workflow_id"] != activeID {
		t.Fatalf("want active %s, got %v", activeID, task["workflow_id"])
	}
}

func TestCompleteTask_crossStateFailureStoresPendingRework(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "plan_review",
		ReworkFeedback: map[string]string{
			"plan_review->planning": "plan_review_to_planner",
		},
		States: map[string]State{
			"plan_review": {
				Role: "qa",
				Transitions: map[string]Transition{
					"failure": {To: "planning"},
				},
			},
			"planning": {
				Role:         "planner",
				Instructions: "fix plan",
				Transitions: map[string]Transition{
					"success": {To: "completed"},
				},
			},
			"completed": {Role: "mayor"},
		},
	})
	id, _ := m.StartWorkflow("rig-flow", map[string]string{"rig": "mockrigb"})
	m.instances[id].CurrentState = "plan_review"

	next, err := m.CompleteTask(id, "failure", "mockrigb/qa", "duplicate main.js beads", "bd list showed 3 open beads")
	if err != nil || next != "planning" {
		t.Fatalf("CompleteTask: next=%q err=%v", next, err)
	}
	inst := m.instances[id]
	if inst.PendingRework == nil || inst.PendingRework.FromState != "plan_review" {
		t.Fatalf("PendingRework: %+v", inst.PendingRework)
	}
	if inst.PendingRework.Summary != "duplicate main.js beads" {
		t.Fatalf("summary: %q", inst.PendingRework.Summary)
	}
	if !strings.Contains(inst.PendingRework.Feedback, "QA summary:") {
		t.Fatalf("feedback should be sanitized summary, got %q", inst.PendingRework.Feedback)
	}

	tpl := m.templates["rig-flow"]
	state, _ := inst.GetCurrentTask(tpl)
	payload, err := m.BuildTaskPayload(inst, tpl, state)
	if err != nil {
		t.Fatal(err)
	}
	rework, ok := payload["pending_rework"].(*WorkflowRework)
	if !ok || rework == nil {
		t.Fatalf("pending_rework: %#v", payload["pending_rework"])
	}
	if rework.FromState != "plan_review" {
		t.Fatalf("rework from_state: %q", rework.FromState)
	}

	_, _ = m.CompleteTask(id, "success", "mockrigb/planner", "", "")
	if inst.PendingRework != nil {
		t.Fatal("success should clear pending rework")
	}
}

func TestCompleteTask_qaArchitectureFailureResetsToDesign(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
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
				Instructions: "fix arch",
				Transitions: map[string]Transition{
					"success": {To: "planning"},
				},
			},
			"planning": {Role: "planner"},
		},
	})
	id, _ := m.StartWorkflow("rig-flow", map[string]string{"rig": "mockrig"})
	m.instances[id].CurrentState = "qa_review"

	next, err := m.CompleteTask(id, "architecture_failure", "mockrig/qa", "unit tests ok; smoke POST 405", "curl smoke failed")
	if err != nil || next != "design" {
		t.Fatalf("CompleteTask: next=%q err=%v", next, err)
	}
	inst := m.instances[id]
	if inst.PendingRework == nil || inst.PendingRework.FromState != "qa_review" {
		t.Fatalf("PendingRework: %+v", inst.PendingRework)
	}
	if !strings.Contains(inst.PendingRework.Feedback, "architecture rework") {
		t.Fatalf("feedback: %q", inst.PendingRework.Feedback)
	}
}

func TestCompleteTask_implementationFailureSkipsPreparePhase(t *testing.T) {
	dir := t.TempDir()
	townRoot := filepath.Join(dir, "gt")
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("arch line\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	prof := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		MinPlanBytes:      50,
		RequiredFiles:     []string{"app/main.go"},
	}
	writeTestRigProfile(t, townRoot, rig, prof)
	plan := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-main: app/main.go",
		"- Scope: main",
		strings.Repeat("- planning sync padding\n", 30),
	}, "\n")
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	setListImplementBeadsByStatusHook(t, townRoot, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "te-main", Title: "Implement app/main.go per architecture"}}, nil
		}
		return nil, nil
	})

	m := NewManager(townRoot)
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "implementation",
		States: map[string]State{
			"implementation": {
				Role: "polecat",
				Transitions: map[string]Transition{
					"failure": {To: "implementation"},
					"success": {To: "qa_review"},
				},
			},
			"qa_review": {Role: "qa"},
		},
	})
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	m.instances[id].CurrentState = "implementation"

	next, err := m.CompleteTask(id, "failure", rig+"/polecat", "tests red", "go test failed")
	if err != nil {
		t.Fatalf("implementation→implementation failure should not re-sync planning: %v", err)
	}
	if next != "implementation" {
		t.Fatalf("next=%q want implementation", next)
	}
}

func TestCompleteTask_sameStateFailureKeepsPendingRework(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "implementation",
		States: map[string]State{
			"implementation": {
				Role: "polecat",
				Transitions: map[string]Transition{
					"success": {To: "qa_review"},
					"failure": {To: "implementation"},
				},
			},
			"qa_review": {Role: "qa", Transitions: map[string]Transition{"failure": {To: "implementation"}}},
		},
	})
	id, _ := m.StartWorkflow("rig-flow", map[string]string{"rig": "mockrig"})
	m.instances[id].CurrentState = "qa_review"
	_, _ = m.CompleteTask(id, "failure", "mockrig/qa", "unittest failed", "ImportError fizzbuzz")
	if m.instances[id].PendingRework == nil {
		t.Fatal("expected pending rework after qa failure")
	}
	m.instances[id].CurrentState = "implementation"
	_, _ = m.CompleteTask(id, "failure", "mockrig/polecat", "", "no outcome after 5 turns")
	if m.instances[id].PendingRework == nil || m.instances[id].PendingRework.Summary != "unittest failed" {
		t.Fatalf("same-state polecat fail should keep QA rework: %+v", m.instances[id].PendingRework)
	}
}
