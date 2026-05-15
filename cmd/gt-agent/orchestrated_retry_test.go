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
	block := formatOrchestratedRetryBlock(prior, task, "testgt2")
	if block == "" {
		t.Fatal("expected retry block")
	}
	if !strings.Contains(block, "te-3xz") || !strings.Contains(block, "Previous attempt") {
		t.Fatalf("block: %q", block)
	}
}

func TestFormatOrchestratedRetryBlock_wrongStateIgnored(t *testing.T) {
	prior := &OrchestratedRetry{WorkflowID: "wf-1", State: "planning", Summary: "x"}
	task := &orchestrator.Task{WorkflowID: "wf-1", State: "implementation"}
	if formatOrchestratedRetryBlock(prior, task, "testgt2") != "" {
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
	h := orchestratedRetryHintsForState("planning", "testgt2")
	if !strings.Contains(h, "testgt2/mayor/rig") || !strings.Contains(h, "plan.md") {
		t.Fatalf("planning hints: %q", h)
	}
}

func TestOrchestratedRetryHints_design(t *testing.T) {
	h := orchestratedRetryHintsForState("design", "testgt2")
	if !strings.Contains(h, "architecture.md") {
		t.Fatalf("design hints: %q", h)
	}
}
