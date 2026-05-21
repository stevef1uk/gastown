package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsRetriableLLMError(t *testing.T) {
	t.Parallel()
	err := errors.New(`LLM API error (status 403): {"error":{"message":"Key limit exceeded (monthly limit)"}}`)
	if !isRetriableLLMError(err) {
		t.Fatal("expected 403 quota to be retriable")
	}
	if isRetriableLLMError(errors.New("context canceled")) {
		t.Fatal("generic errors should not be retriable")
	}
}

func TestRejectImplementationNoOpFailure_blocksWithoutFixWork(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "qa_review",
			Summary:   "GET /api/links returned 404",
		},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	msg, reject := r.rejectImplementationNoOpFailure("failure")
	if !reject || !strings.Contains(msg, "Rejected") || !strings.Contains(msg, "EDIT:") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationNoOpFailure_allowsAfterFixWork(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 1, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.attemptFixWork = true
	if _, reject := r.rejectImplementationNoOpFailure("failure"); reject {
		t.Fatal("should allow failure after fix attempt")
	}
}

func TestNoteImplementationFixAttempt_bdUpdate(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.noteImplementationFixAttempt("export BEADS_DIR=x && bd update te-d0t --status=in_progress", false)
	if !r.attemptFixWork {
		t.Fatal("bd update should count as fix work")
	}
	r.attemptFixWork = false
	r.noteImplementationFixAttempt("cat mockrig/mayor/rig/SPEC.md", false)
	if r.attemptFixWork {
		t.Fatal("read-only cat should not count as fix work")
	}
}
