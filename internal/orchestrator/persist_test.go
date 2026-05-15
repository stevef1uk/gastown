package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistInstancesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	wf, err := m.StartWorkflow("missing", nil)
	_ = wf
	if err == nil {
		t.Fatal("expected missing template error")
	}

	tpl := &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "kickoff",
		States: map[string]State{
			"kickoff": {Role: "mayor", Instructions: "go", Transitions: map[string]Transition{
				"success": {To: "completed"},
			}},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	}
	m.LoadTemplate(tpl)

	wf, err = m.StartWorkflow("rig-flow", map[string]string{"rig": "testgt2"})
	if err != nil {
		t.Fatal(err)
	}

	m2 := NewManager(dir)
	if len(m2.instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(m2.instances))
	}
	if m2.instances[wf].Variables["rig"] != "testgt2" {
		t.Fatalf("rig var: %v", m2.instances[wf].Variables)
	}

	path := InstancesPath(dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("instances file: %v", err)
	}
	_ = filepath.Base(path)
}
