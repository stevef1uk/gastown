package orchestrator

import (
	"strings"
	"testing"
)

func TestIsPlanGapPlaceholderPath(t *testing.T) {
	placeholders := []string{
		"plan_gap", "plan-gap", "plan gap", "TBD", "todo", "none", "n/a", "na", "pending", "placeholder",
		"Plan_Gap", "PLAN_GAP", "pingapp/plan_gap",
	}
	for _, p := range placeholders {
		if !isPlanGapPlaceholderPath(p) {
			t.Errorf("expected %q to be treated as a placeholder", p)
		}
	}
	real := []string{
		"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py",
		"pingapp/tests/test_api.py", "cmd/server/main.go", "requirements.py",
		"plan_gap.py", "pingapp/plan_gap.txt", "plan_todo.txt",
	}
	for _, f := range real {
		if isPlanGapPlaceholderPath(f) {
			t.Errorf("expected %q to be treated as a real file path", f)
		}
	}
}
func TestFilterNonImplementPaths(t *testing.T) {
	in := []string{
		"pingapp/requirements.txt",
		"pingapp/plan_gap",
		"plan_gap",
		"plan.md",
		"architecture.md",
		"pingapp/main.py",
		"",
	}
	got := FilterNonImplementPaths(in)
	want := []string{"pingapp/requirements.txt", "pingapp/main.py"}
	if len(got) != len(want) {
		t.Fatalf("FilterNonImplementPaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("FilterNonImplementPaths[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHealPhaseVerifyTestFiles_DoesNotDuplicateOwnedTest(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    "pingapp",
		RequiredFiles: []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "python-setup", Title: "python-setup", RequiredFiles: []string{"pingapp/requirements.txt"}},
			// core legitimately runs pytest (shared suite) but owns ONLY main.py —
			// test_main.py belongs to the test phase.
			{ID: "core", Title: "core", RequiredFiles: []string{"pingapp/main.py"}, QAVerifyCommand: "cd pingapp && pytest"},
			{ID: "test", Title: "test", RequiredFiles: []string{"pingapp/test_main.py"}, QAVerifyCommand: "cd pingapp && python -m pytest -v"},
			{ID: "integration-test", Title: "integration-test", RequiredFiles: []string{}, QAVerifyCommand: "cd pingapp && echo ok"},
		},
	}
	out := SanitizeRigFlowProfile(v)
	for _, p := range out.DeliveryPhases {
		switch p.ID {
		case "core":
			if len(p.RequiredFiles) != 1 || p.RequiredFiles[0] != "pingapp/main.py" {
				t.Errorf("core required_files = %v, want [pingapp/main.py]", p.RequiredFiles)
			}
		case "test":
			found := false
			for _, f := range p.RequiredFiles {
				if strings.HasSuffix(f, "test_main.py") {
					found = true
				}
			}
			if !found {
				t.Errorf("test phase should own test_main.py, got %v", p.RequiredFiles)
			}
		}
	}
}

func TestPairPhaseTests_PreservesTestPhaseOwnership(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    "pingapp",
		RequiredFiles: []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "python-setup", Title: "python-setup", RequiredFiles: []string{"pingapp/requirements.txt"}},
			{ID: "core", Title: "core", RequiredFiles: []string{"pingapp/main.py"}, QAVerifyCommand: "cd pingapp && python -c 'import sys; print(\"ok\")'"},
			// SPEC/architecture deliberately assign the unit test to the test phase
			// while its source (main.py) lives in core — pairPhaseTests must not
			// steal the test into core or empty the test phase.
			{ID: "test", Title: "test", RequiredFiles: []string{"pingapp/test_main.py"}, QAVerifyCommand: "cd pingapp && python -m pytest -v"},
			{ID: "integration-test", Title: "integration-test", RequiredFiles: []string{}},
		},
	}
	out := ClampProfileValidation(v)
	coreHasTest := false
	testHasTest := false
	var testFiles []string
	for _, p := range out.DeliveryPhases {
		switch p.ID {
		case "core":
			for _, f := range p.RequiredFiles {
				if strings.HasSuffix(f, "test_main.py") {
					coreHasTest = true
				}
			}
		case "test":
			testFiles = p.RequiredFiles
			for _, f := range p.RequiredFiles {
				if strings.HasSuffix(f, "test_main.py") {
					testHasTest = true
				}
			}
		}
	}
	if coreHasTest {
		t.Errorf("core should NOT own test_main.py after clamp, got it in core.required_files")
	}
	if !testHasTest {
		t.Errorf("test phase should own test_main.py, got %v", testFiles)
	}
}

func TestPromptVars_ExcludesSetupOnlyFromTestValidation(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    "pingapp",
		RequiredFiles: []string{"pingapp/requirements.txt", "pingapp/test_main.py"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "python-setup", Title: "python-setup", RequiredFiles: []string{"pingapp/requirements.txt"}, QAVerifyCommand: "cd pingapp && echo 'verify ok (no automated tests for this phase)'"},
			{ID: "test", Title: "test", RequiredFiles: []string{"pingapp/test_main.py"}, QAVerifyCommand: "cd pingapp && python -m pytest -v"},
		},
		ActivePhaseIDField:   "python-setup",
		CompletedPhaseIDsField: nil,
	}
	vars := v.PromptVars()
	got := vars["test_validation_phase_ids"]
	// Setup-only active phase must be excluded; no test phase remains, so empty.
	if strings.Contains(got, "python-setup") {
		t.Errorf("test_validation_phase_ids should exclude setup-only active phase, got %q", got)
	}
}
