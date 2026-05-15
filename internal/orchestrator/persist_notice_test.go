package orchestrator

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuildRestoreNotice_empty(t *testing.T) {
	dir := t.TempDir()
	n, err := BuildRestoreNotice(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n.Count != 0 {
		t.Fatalf("expected 0, got %d", n.Count)
	}
}

func TestBuildRestoreNotice_andDuplicateWarning(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	tpl := &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "kickoff",
		States: map[string]State{
			"kickoff": {Role: "mayor", Instructions: "go", Transitions: map[string]Transition{
				"success": {To: "design"},
			}},
			"design": {Role: "architect", Instructions: "go"},
		},
	}
	m.LoadTemplate(tpl)
	if _, err := m.StartWorkflow("rig-flow", map[string]string{"rig": "testgt2"}); err != nil {
		t.Fatal(err)
	}
	// Simulate legacy duplicate instances (StartWorkflow now rejects a second active).
	m.mu.Lock()
	id2 := m.allocateWorkflowID()
	m.instances[id2] = &WorkflowInstance{
		ID: id2, TemplateID: "rig-flow", CurrentState: "kickoff",
		Variables: map[string]string{"rig": "testgt2"}, Status: "running",
	}
	m.mu.Unlock()
	for _, inst := range m.instances {
		inst.CurrentState = "implementation"
	}
	_ = m.persistLocked()

	n, err := BuildRestoreNotice(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n.Count != 2 {
		t.Fatalf("count=%d", n.Count)
	}

	var buf bytes.Buffer
	n.WriteStartupLog(&buf)
	if !strings.Contains(buf.String(), "Restored 2 workflow") {
		t.Fatalf("log: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "resume, not new") {
		t.Fatalf("missing resume hint: %q", buf.String())
	}

	statuses, _ := m.GetWorkflowStatus("")
	warn := DuplicateActiveWarning(statuses)
	if !strings.Contains(warn, "2 active for rig-flow/testgt2") {
		t.Fatalf("warn=%q", warn)
	}

	summary := WorkflowResumeSummary(dir)
	if !strings.Contains(summary, "implementation") {
		t.Fatalf("summary=%q", summary)
	}
}
