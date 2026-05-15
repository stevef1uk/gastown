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
	id, err := m.StartWorkflow(tpl.ID, map[string]string{"rig": "testgt2"})
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
	vars := map[string]string{"rig": "testgt2"}
	if !AgentMatchesTask("testgt2/architect", "architect", vars) {
		t.Fatal("rig-qualified agent should match")
	}
	if AgentMatchesTask("architect", "architect", vars) {
		t.Fatal("bare architect should not match when workflow has rig")
	}
	if !AgentMatchesTask("mayor", "mayor", vars) {
		t.Fatal("town mayor should match when workflow has rig")
	}
	if !AgentMatchesTask("planner", "planner", vars) {
		t.Fatal("town planner should match when workflow has rig")
	}
	if AgentMatchesTask("planner", "architect", vars) {
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
	m.CompleteTask(id, "success")
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

	next, err := m.CompleteTask(id, "success")
	if err != nil || next != "planning" {
		t.Fatalf("CompleteTask success: next=%q err=%v", next, err)
	}
	if m.instances[id].CurrentState != "planning" {
		t.Fatalf("state: %q", m.instances[id].CurrentState)
	}

	next, err = m.CompleteTask(id, "success")
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

	next, err := m.CompleteTask(id, "fail")
	if err != nil || next != "design" {
		t.Fatalf("fail alias: next=%q err=%v", next, err)
	}
}

func TestCompleteTask_invalidOutcome(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	_, err := m.CompleteTask(id, "bogus")
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

func TestHasActiveWorkflow(t *testing.T) {
	m, id := loadTestManager(t, designFlowTemplate())

	if !m.HasActiveWorkflow("rig-flow", "testgt2") {
		t.Fatal("expected active rig-flow for testgt2")
	}
	if m.HasActiveWorkflow("rig-flow", "other") {
		t.Fatal("should not match other rig")
	}
	if m.HasActiveWorkflow("other-tpl", "testgt2") {
		t.Fatal("should not match other template")
	}

	_, _ = m.CompleteTask(id, "success")
	_, _ = m.CompleteTask(id, "success")
	if m.HasActiveWorkflow("rig-flow", "testgt2") {
		t.Fatal("completed workflow should not be active")
	}
}

func TestValidateTemplateSchema(t *testing.T) {
	warn := validateTemplateSchema(&WorkflowTemplate{
		ID: "bad",
		States: map[string]State{
			"intake": {Instructions: "no role field"},
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
	m.CompleteTask(doneID, "success")
	activeID, _ := m.StartWorkflow("x", nil)
	task, err := m.FetchTask("mayor")
	if err != nil {
		t.Fatal(err)
	}
	if task["workflow_id"] != activeID {
		t.Fatalf("want active %s, got %v", activeID, task["workflow_id"])
	}
}
