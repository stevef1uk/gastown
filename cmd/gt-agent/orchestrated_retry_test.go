package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestFormatOrchestratedRetryBlock_matchesWorkflowState(t *testing.T) {
	prior := &OrchestratedRetry{
		WorkflowID: "wf-1",
		State:      "implementation",
		Outcome:    "failure",
		Summary:    "bead te-3xz not found",
		Feedback:   "Error: no issue found matching te-3xz",
	}
	task := &orchestrator.Task{WorkflowID: "wf-1", State: "implementation"}
	block := formatOrchestratedRetryBlock(prior, task, "mockrig")
	if block == "" {
		t.Fatal("expected retry block")
	}
	if !strings.Contains(block, "te-3xz") || !strings.Contains(block, "Previous attempt") {
		t.Fatalf("block: %q", block)
	}
}

func TestValidatePlanReviewGrep_rejectsBadPatterns(t *testing.T) {
	cases := []struct {
		cmd string
		ok  bool
	}{
		{"grep -E 'te-8i9|te-9dw'", false},
		{"grep te-8i9 beads.md", false},
		{"grep -i implement plan.md", true},
		{"grep backend architecture.md", true},
	}
	for _, tc := range cases {
		err := validatePlanReviewGrep(tc.cmd)
		if tc.ok && err != nil {
			t.Errorf("%q: unexpected error: %v", tc.cmd, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q: expected rejection", tc.cmd)
		}
	}
}

func TestFormatWorkflowReworkBlock_planOKSkipsRewriteHint(t *testing.T) {
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "planning",
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "plan_review",
			Outcome:   "failure",
			Summary:   "duplicate beads; plan.md size 1020 bytes (≥1000) ok",
			Feedback:  "QA summary: duplicate beads; plan.md size 1020 bytes (≥1000) ok",
		},
	}
	block := formatWorkflowReworkBlock(task, "", "mockrig")
	if !strings.Contains(block, "plan.md is OK") || strings.Contains(block, "Rewrite `plan.md`") {
		t.Fatalf("expected plan-ok hints only, got:\n%s", block)
	}
}

func TestFormatWorkflowReworkBlock_qaToPlanner(t *testing.T) {
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "planning",
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "plan_review",
			Outcome:   "failure",
			Summary:   "duplicate main.js beads",
			Feedback:  "FAIL: missing bead for backend/main.py",
			AgentID:   "mockrigb/qa",
		},
	}
	block := formatWorkflowReworkBlock(task, "", "mockrigb")
	if block == "" {
		t.Fatal("expected rework block")
	}
	if !strings.Contains(block, "plan_review") || !strings.Contains(block, "duplicate main.js beads") {
		t.Fatalf("block: %q", block)
	}
}

func TestFormatWorkflowReworkBlock_sameStateIgnored(t *testing.T) {
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "implementation",
			Summary:   "x",
		},
	}
	if formatWorkflowReworkBlock(task, "", "mockrigb") != "" {
		t.Fatal("same-state rework should use OrchestratedRetry, not workflow block")
	}
}

func TestFormatOrchestratedRetryBlock_wrongStateIgnored(t *testing.T) {
	prior := &OrchestratedRetry{WorkflowID: "wf-1", State: "planning", Summary: "x"}
	task := &orchestrator.Task{WorkflowID: "wf-1", State: "implementation"}
	if formatOrchestratedRetryBlock(prior, task, "mockrig") != "" {
		t.Fatal("should not inject planning failure into implementation")
	}
}

func TestUpdateOrchestratedRetry_failureThenSuccess(t *testing.T) {
	state := AgentState{}
	task := &orchestrator.Task{WorkflowID: "wf-1", TemplateID: "rig-flow", State: "implementation"}
	updateOrchestratedRetry(&state, task, "failure", "bad bead", "Command failed: te-xyz")
	if state.OrchestratedRetry == nil || state.OrchestratedRetry.Summary != "bad bead" {
		t.Fatalf("expected retry stored: %+v", state.OrchestratedRetry)
	}
	updateOrchestratedRetry(&state, task, "success", "bead te-aba completed", "")
	if state.OrchestratedRetry != nil {
		t.Fatal("success should clear retry for same step")
	}
}

func TestOrchestratedRetryHints_planningMentionsWorktree(t *testing.T) {
	task := &orchestrator.Task{
		Hooks: orchestrator.StateHooks{
			RetryHint: "After `cd {{rig}}/mayor/rig`, write plan.md",
		},
	}
	h := newStateRunner(task, "", "mockrig").retryHint()
	if !strings.Contains(h, "mockrig/mayor/rig") || !strings.Contains(h, "plan.md") {
		t.Fatalf("planning hints: %q", h)
	}
}

func TestOrchestratedRetryHints_design(t *testing.T) {
	task := &orchestrator.Task{
		Hooks: orchestrator.StateHooks{
			RetryHint: "write architecture.md via heredoc",
		},
	}
	h := newStateRunner(task, "", "mockrig").retryHint()
	if !strings.Contains(h, "architecture.md") {
		t.Fatalf("design hints: %q", h)
	}
}
