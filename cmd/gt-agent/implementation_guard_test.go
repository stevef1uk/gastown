package main

import (
	"errors"
	"os"
	"path/filepath"
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
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 2, nil }
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
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 1, nil }
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

func TestRejectImplementationNoOpFailure_blocksAfterEditSearchMiss(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 3, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.attemptEditSearchMiss = true
	msg, reject := r.rejectImplementationNoOpFailure("failure")
	if !reject || !strings.Contains(msg, "SEARCH") || !strings.Contains(msg, "Auto-READ") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationPrematureSuccess_blocksUnusedImport(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = false
	r.track.hadCmdFailure = true
	r.track.lastVerifyOutput = `./handlers_test.go:6:2: "fmt" imported and not used`
	msg, reject := r.rejectImplementationPrematureSuccess("success")
	if !reject || !strings.Contains(msg, "Rejected") || !strings.Contains(msg, "goimports") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}
}

func TestRejectImplementationPrematureSuccess_allowsAfterVerifyOK(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 1, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	r.track.lastVerifyOutput = "ok"
	if _, reject := r.rejectImplementationPrematureSuccess("success"); reject {
		t.Fatal("should allow success when verifyOK")
	}
}

func TestTryAutoOutcome_blockedOnQAPendingRework(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) { return 0, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			QAVerifyCommand: "cd linkshelf && go test ./...",
		},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "qa_review",
			Summary:   "runtime smoke failed: GET / 404",
		},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	if _, _, ok := r.tryAutoOutcome(); ok {
		t.Fatal("must not auto-complete implementation while QA rework is pending")
	}
}

func TestSummaryClaimsFalseBeadInfraFailure(t *testing.T) {
	if !summaryClaimsFalseBeadInfraFailure("Bead system corruption detected; reset .beads") {
		t.Fatal("expected corruption claim")
	}
	if summaryClaimsFalseBeadInfraFailure("handlers_test.go undefined: getLinksHandler") {
		t.Fatal("compile errors are not bead infra claims")
	}
}

func TestRejectImplementationFalseBeadInfraFailure_blocksHallucination(t *testing.T) {
	townRoot := "/home/stevef/gt"
	rig := "testgt3"
	if _, err := os.Stat(filepath.Join(townRoot, rig, "mayor", "rig")); err != nil {
		t.Skip("testgt3 rig not present")
	}
	if !orchestrator.BeadsDatabaseReady(townRoot, rig) {
		t.Skip("testgt3 beads DB not ready")
	}
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
	}
	r := newStateRunner(task, townRoot, rig)
	msg, reject := r.rejectImplementationFalseBeadInfraFailure("failure", "Bead system corruption detected; reset .beads")
	if !reject || !strings.Contains(msg, "do **not** reset") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
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
