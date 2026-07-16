package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestImplementationArtifactFailureExtra_allClosedAfterQA(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Artifacts: "implementation"},
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "qa_review",
			Summary:   "POST /api/links returned 405",
		},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.v = orchestrator.WorkflowValidation{BeadTitleContains: "Implement mock/"}
	err := validateImplementationArtifacts(r.townRoot, r.rig, true, false, false, r.v)
	msg := r.implementationArtifactFailureExtra(err)
	if !strings.Contains(msg, "All implement beads are closed") {
		t.Fatalf("want closed-bead guidance: %q", msg)
	}
	if !strings.Contains(msg, "Prior step failed") || !strings.Contains(msg, "bd update") {
		t.Fatalf("want QA rework reopen steps: %q", msg)
	}
}

func TestValidateOutcomeForTask_shellErrorNoLongerRejected(t *testing.T) {
	task := &orchestrator.Task{
		State: "qa_review",
		Hooks: orchestrator.StateHooks{BeadIDsInSummary: false},
	}
	// Shell-error summaries must NOT be rejected — the orchestrator handles them.
	for _, summary := range []string{
		"syntax error",
		"syntaxerror",
		"command not found",
		"no such file",
		"pytest failed with exit status 2, syntax error in command",
	} {
		if err := validateOutcomeForTask(task, t.TempDir(), "mockrig", "failure", summary); err != nil {
			t.Fatalf("summary %q must not be rejected: %v", summary, err)
		}
	}
}

func TestImplementationArtifactFailureExtra_notForPlanning(t *testing.T) {
	task := &orchestrator.Task{
		State: "planning",
		Hooks: orchestrator.StateHooks{Artifacts: "planning"},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	err := validateImplementationArtifacts(r.townRoot, r.rig, true, false, false, r.v)
	if msg := r.implementationArtifactFailureExtra(err); msg != "" {
		t.Fatalf("planning should not get implementation extra: %q", msg)
	}
}
