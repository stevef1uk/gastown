package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// errFake is a fake command error used in track handler tests.
// It is defined in implement_guards_test.go in the same package.
func TestReviewTrack_readOnlyProbeDoesNotSetHadCmdFailure(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		State:      "test_review",
		Hooks:      orchestrator.StateHooks{Track: "test_review"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, dir, rig)
	var cmdErr error = errFake{}
	// Read-only ls command that fails — with Fix A this should NOT set hadCmdFailure.
	trackHandlers["test_review"](r, "ls pingapp/main.go", cmdErr)
	if r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure to be false for failed read-only probe")
	}
}

func TestReviewTrack_nonReadOnlyFailureSetsHadCmdFailure(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		State:      "test_review",
		Hooks:      orchestrator.StateHooks{Track: "test_review"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, dir, rig)
	var cmdErr error = errFake{}
	// Non-read-only command failure — should still set hadCmdFailure.
	trackHandlers["test_review"](r, "go test ./...", cmdErr)
	if !r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure to be true for failed non-read-only command")
	}
}

func TestQATrack_readOnlyProbeDoesNotSetHadCmdFailure(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		State:      "qa",
		Hooks:      orchestrator.StateHooks{Track: "qa"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, dir, rig)
	var cmdErr error = errFake{}
	// Read-only wc command that fails — with Fix A this should NOT set hadCmdFailure.
	trackHandlers["qa"](r, "wc -l TEST_PLAN.md", cmdErr)
	if r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure to be false for failed read-only probe in qa")
	}
}

func TestPlanTrack_readOnlyProbeDoesNotSetHadCmdFailure(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		State:      "test_plan",
		Hooks:      orchestrator.StateHooks{Track: "test_plan"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, dir, rig)
	var cmdErr error = errFake{}
	// Read-only cat command that fails — with Fix A this should NOT set hadCmdFailure.
	trackHandlers["test_plan"](r, "cat nonexistent.md", cmdErr)
	if r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure to be false for failed read-only probe in test_plan")
	}
}

func TestPlanTrack_bdListReadOnlyDoesNotSetHadCmdFailure(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		State:      "test_plan",
		Hooks:      orchestrator.StateHooks{Track: "test_plan"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, dir, rig)
	var cmdErr error = errFake{}
	// bd list is a read-only QA command — with Fix A this should NOT set hadCmdFailure.
	trackHandlers["test_plan"](r, "bd list", cmdErr)
	if r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure to be false for read-only bd list probe in test_plan")
	}
}