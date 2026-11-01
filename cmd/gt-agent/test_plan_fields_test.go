package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestMissingTestPlanRequiredFields(t *testing.T) {
	phases := []orchestrator.DeliveryPhase{
		{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}},
		{ID: "core", RequiredFiles: []string{"pingapp/main.go"}},
		{ID: "test", RequiredFiles: []string{"pingapp/main_test.go"}},
	}
	v := orchestrator.WorkflowValidation{DeliveryPhases: phases}

	blocks := []orchestrator.TestPlanBlock{
		// Manifest phase row: empty Test file is valid per test_plan.md hard rule.
		{ReqID: "go-module", Level: "unit", TestFile: "", Phase: "go-module", BeadID: "pr-e0v"},
		// Production-only phase row: empty Test file is also valid.
		{ReqID: "core", Level: "unit", TestFile: "", Phase: "core", BeadID: "pr-e1"},
		// Test-carrying phase row: empty Test file IS a missing field.
		{ReqID: "test", Level: "unit", TestFile: "", Phase: "test", BeadID: "pr-e2"},
	}
	got := missingTestPlanRequiredFields(v, blocks)
	if len(got) != 1 || got[0] != "test" {
		t.Fatalf("want only [test] missing, got %v", got)
	}
}

func TestMissingTestPlanRequiredFields_phaseFallsBackToReqID(t *testing.T) {
	phases := []orchestrator.DeliveryPhase{
		{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}},
	}
	v := orchestrator.WorkflowValidation{DeliveryPhases: phases}

	// Legacy plans omit the Phase field; the section heading (ReqID) must be
	// used to resolve the phase before deciding whether Test file is required.
	blocks := []orchestrator.TestPlanBlock{
		{ReqID: "go-module", Level: "unit", TestFile: "", BeadID: "pr-e0v"},
	}
	if got := missingTestPlanRequiredFields(v, blocks); len(got) != 0 {
		t.Fatalf("empty Test file for manifest phase via ReqID must pass, got %v", got)
	}
}

func TestMissingTestPlanRequiredFields_unknownPhaseStillRequiresTestFile(t *testing.T) {
	blocks := []orchestrator.TestPlanBlock{
		{ReqID: "req-9", Level: "unit", TestFile: ""},
	}
	got := missingTestPlanRequiredFields(orchestrator.WorkflowValidation{}, blocks)
	if len(got) != 1 || got[0] != "req-9" {
		t.Fatalf("unresolvable row must keep old behavior, got %v", got)
	}
}

func TestMissingTestPlanRequiredFields_levelAlwaysRequired(t *testing.T) {
	phases := []orchestrator.DeliveryPhase{
		{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}},
	}
	v := orchestrator.WorkflowValidation{DeliveryPhases: phases}

	blocks := []orchestrator.TestPlanBlock{
		{ReqID: "go-module", Level: "", TestFile: ""},
	}
	if got := missingTestPlanRequiredFields(v, blocks); len(got) != 1 {
		t.Fatalf("missing Level must be reported even for manifest phase, got %v", got)
	}
}
