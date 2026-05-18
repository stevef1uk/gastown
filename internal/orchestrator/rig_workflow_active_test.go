package orchestrator

import "testing"

func TestRigWorkflowActivityFromStatuses(t *testing.T) {
	statuses := []WorkflowStatus{
		{ID: "wf-1", Variables: map[string]string{"rig": "testgt3"}, Status: "paused"},
		{ID: "wf-2", Variables: map[string]string{"rig": "testgt4"}, Status: "running"},
	}
	if got := rigWorkflowActivityFromStatuses("testgt3", statuses); got != RigWorkflowPaused {
		t.Fatalf("testgt3 = %q want paused", got)
	}
	if got := rigWorkflowActivityFromStatuses("testgt4", statuses); got != RigWorkflowRunning {
		t.Fatalf("testgt4 = %q want running", got)
	}
	if got := rigWorkflowActivityFromStatuses("other", statuses); got != RigWorkflowIdle {
		t.Fatalf("other = %q want idle", got)
	}
}

func TestSkipRigAgentStartReason_requiresOrchestrator(t *testing.T) {
	// Empty town root — orchestrator not running
	if SkipRigAgentStartReason(t.TempDir(), "testgt3") != "" {
		t.Fatal("expected no skip when orchestrator down")
	}
}
