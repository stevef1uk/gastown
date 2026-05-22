package feed

import (
	"os"
	"path/filepath"
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
	got, err := LoadWorkflowsByRig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["testgt3"]) != 1 {
		t.Fatalf("testgt3: %+v", got["testgt3"])
	}
	w := got["testgt3"][0]
	if w.ID != "wf-2" || w.CurrentState != "implementation" || w.Status != "running" {
		t.Fatalf("got %+v", w)
	}
}
