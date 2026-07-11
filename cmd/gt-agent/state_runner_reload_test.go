package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// TestStateRunner_reloadValidationIfPhaseChanged verifies that when the active
// delivery phase changes on disk between turns, the stateRunner reloads its
// WorkflowValidation and prompt vars so path guards use the current phase.
func TestStateRunner_reloadValidationIfPhaseChanged(t *testing.T) {
	town := t.TempDir()
	rig := "myrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, ".gastown"), 0755); err != nil {
		t.Fatal(err)
	}

	v := orchestrator.WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement ",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "backend", RequiredFiles: []string{"backend/main.py"}},
			{ID: "frontend", RequiredFiles: []string{"frontend/app.tsx"}},
		},
		ActivePhaseIDField: "backend",
	}
	if err := orchestrator.WriteRigWorkflowProfile(town, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}

	task := rigFlowTask(t, "implementation", orchestrator.DefaultWorkflowValidation())
	task.Rig = rig
	r := newStateRunner(task, town, rig)
	if r.v.ActivePhaseID() != "backend" {
		t.Fatalf("initial active phase = %q, want backend", r.v.ActivePhaseID())
	}
	if _, ok := r.promptVars["qa_runtime_smoke_block"]; !ok {
		t.Fatal("qa_runtime_smoke_block missing from initial prompt vars")
	}

	// Change active phase on disk to frontend.
	if err := orchestrator.SetRigActivePhase(town, rig, "frontend"); err != nil {
		t.Fatal(err)
	}

	if !r.reloadValidationIfPhaseChanged() {
		t.Fatal("expected reload when active phase changed on disk")
	}
	if r.v.ActivePhaseID() != "frontend" {
		t.Fatalf("reloaded active phase = %q, want frontend", r.v.ActivePhaseID())
	}
	if got := r.v.RequiredFiles; len(got) != 1 || got[0] != "frontend/app.tsx" {
		t.Fatalf("reloaded required files = %v, want [frontend/app.tsx]", got)
	}
	if _, ok := r.promptVars["qa_runtime_smoke_block"]; !ok {
		t.Fatal("qa_runtime_smoke_block missing from reloaded prompt vars")
	}

	// No change — second call should be a no-op.
	if r.reloadValidationIfPhaseChanged() {
		t.Fatal("expected no reload when active phase has not changed")
	}
}
