package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateTesterCommand_AllowsConfiguredPipInstallInTestReview(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "pingapp",
		ActivePhaseIDField: "python-setup",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{
				ID:              "python-setup",
				RequiredFiles:   []string{"pingapp/requirements.txt"},
				QAVerifyCommand: "cd pingapp && pip install -r requirements.txt",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"pingapp/test_main.py"},
				QAVerifyCommand: "cd pingapp && pytest",
			},
		},
	}

	// Configured phase verify uses pip install: allowed in test_review.
	if err := validateTesterCommand("cd ping_rig/mayor/rig/pingapp && pip install -r requirements.txt", "ping_rig", t.TempDir(), "test_review", v); err != nil {
		t.Fatalf("expected configured pip install verify to be allowed in test_review, got: %v", err)
	}

	// Still blocked in test_plan (not running verify).
	if err := validateTesterCommand("cd ping_rig/mayor/rig/pingapp && pip install -r requirements.txt", "ping_rig", t.TempDir(), "test_plan", v); err == nil {
		t.Fatal("expected pip install to remain blocked in test_plan")
	}

	// Still blocked in test_review when active phase verify does NOT use pip install.
	v.ActivePhaseIDField = "test"
	if err := validateTesterCommand("cd ping_rig/mayor/rig/pingapp && pip install -r requirements.txt", "ping_rig", t.TempDir(), "test_review", v); err == nil {
		t.Fatal("expected ad-hoc pip install to remain blocked in non-install phases")
	}

	// QA command guard mirrors the same rule: allowed when active phase verify uses pip install, blocked otherwise.
	v.ActivePhaseIDField = "python-setup"
	if err := validateQACommand("cd ping_rig/mayor/rig/pingapp && pip install -r requirements.txt", "ping_rig", t.TempDir(), v); err != nil {
		t.Fatalf("expected configured pip install verify to be allowed in QA step, got: %v", err)
	}
	v.ActivePhaseIDField = "test"
	if err := validateQACommand("cd ping_rig/mayor/rig/pingapp && pip install -r requirements.txt", "ping_rig", t.TempDir(), v); err == nil {
		t.Fatal("expected ad-hoc pip install to remain blocked in QA step for non-install phases")
	}
}
