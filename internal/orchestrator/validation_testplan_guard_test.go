package orchestrator

import "testing"

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