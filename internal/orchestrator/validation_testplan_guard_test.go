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
