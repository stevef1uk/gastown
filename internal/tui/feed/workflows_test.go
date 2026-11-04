package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadWorkflowsByRig_fromInstancesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "orchestrator")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	data := `{
  "instances": [
    {
      "id": "wf-2",
      "template_id": "rig-flow",
      "current_state": "implementation",
      "status": "running",
      "variables": {"rig": "testgt3"}
    },
    {
      "id": "wf-1",
      "template_id": "rig-flow",
      "current_state": "completed",
      "status": "completed",
      "variables": {"rig": "other"}
    }
  ],
  "next_seq": 2
}`
	if err := os.WriteFile(filepath.Join(path, "instances.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(dir, "testgt3", "mayor", "rig", ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := `{"validation":{"active_phase_id":"backend-core","delivery_phases":[{"id":"setup"},{"id":"backend-core"}]}}`
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadWorkflowsByRig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["testgt3"]) != 1 {
		t.Fatalf("testgt3: %+v", got["testgt3"])
	}
	w := got["testgt3"][0]
	if w.ID != "wf-2" || w.DeliveryPhase != "backend-core" || w.CurrentState != "implementation" || w.Status != "running" {
		t.Fatalf("got %+v", w)
	}
	if w.DeliveryPhaseIndex != 2 || w.DeliveryPhaseTotal != 2 {
		t.Fatalf("expected phase 2 of 2: %+v", w)
	}
	m := &Model{}
	if line := m.renderWorkflowLine(w); !strings.Contains(line, "rig-flow  backend-core (2/2)  implementation") {
		t.Fatalf("phase should appear between workflow name and state: %q", line)
	}
	if len(got["other"]) != 1 || got["other"][0].DeliveryPhase != "" {
		t.Fatalf("workflow without a profile: %+v", got["other"])
	}
}
