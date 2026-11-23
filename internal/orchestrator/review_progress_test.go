package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordReviewFailure_contractMismatchPausesImmediatelyAfterRepeat(t *testing.T) {
	inst := &WorkflowInstance{}
	for i, want := range []reviewFailureAction{reviewFailureContinue, reviewFailurePause} {
		got := inst.recordReviewFailure("test_review", "phase-1", "contract_mismatch", 3, "review_recovery")
		if got != want {
			t.Fatalf("attempt %d: action=%v want=%v", i+1, got, want)
		}
	}
	if got := inst.ReviewFailures["test_review:phase-1"]; got != contractMismatchReviewLimit {
		t.Fatalf("failures=%d want=%d", got, contractMismatchReviewLimit)
	}
}

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
	if !strings.Contains(inst.PauseReason, "review failed 3 times without changing") {
		t.Fatalf("pause reason missing no-progress cause: %q", inst.PauseReason)
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
