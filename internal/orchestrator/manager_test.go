package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
