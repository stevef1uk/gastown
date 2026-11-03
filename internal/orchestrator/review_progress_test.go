package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewNoProgressPersistsAndPauses(t *testing.T) {
	town, rig := t.TempDir(), "example"
	dir := filepath.Join(town, rig, "mayor", "rig", ".gastown")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "workflow-profile.json")
	if err := os.WriteFile(profile, []byte(`{"delivery_phases":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	hooks := StateHooks{NoProgressLimit: 3, ProgressFiles: []string{".gastown/workflow-profile.json"}}
	inst := WorkflowInstance{Status: "running", PendingRework: &WorkflowRework{Summary: "incomplete profile"}}
	for i := 0; i < 3; i++ {
		// Simulate orchestrator restart between reviews.
		raw, _ := json.Marshal(inst)
		if err := json.Unmarshal(raw, &inst); err != nil {
			t.Fatal(err)
		}
		inst.recordReviewProgress(town, rig, "review", "failure", hooks)
		if i < 2 && inst.Status != "running" {
			t.Fatal("paused before the configured retry budget")
		}
	}
	if inst.Status != "paused" || !strings.Contains(inst.PendingRework.Feedback, "without changing") {
		t.Fatalf("unchanged contract kept looping: %+v", inst)
	}
	inst.Status = "running"
	if err := os.WriteFile(profile, []byte(`{"delivery_phases":[{"id":"core"}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	inst.recordReviewProgress(town, rig, "review", "failure", hooks)
	if inst.Status != "running" || inst.ReviewProgress["review"].Failures != 1 {
		t.Fatal("real contract progress did not reset the budget")
	}
	inst.recordReviewProgress(town, rig, "review", "success", hooks)
	if len(inst.ReviewProgress) != 0 {
		t.Fatal("successful review retained failure budget")
	}
}
