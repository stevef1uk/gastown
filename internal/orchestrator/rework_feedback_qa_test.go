package orchestrator

import (
	"strings"
	"testing"
)

func TestPrepareQAReviewToImplementationFeedback(t *testing.T) {
	summary := "pytest failed; stub myapp/backend/main.py; reopen beads from bd list"
	raw := "Command: pytest -q\nError: exit status 127\n✓ xx-b3w [task] - Implement myapp/backend/requirements.txt\nSyntaxError: invalid syntax\n"
	v := WorkflowValidation{
		LayoutRoot:    "myapp",
		RequiredFiles: []string{"myapp/backend/main.py", "myapp/backend/requirements.txt"},
		QAVerifyCommand: "cd myapp/backend && python3 -m pytest -q",
	}
	got := prepareQAReviewToImplementationFeedback(summary, raw, v)
	if !strings.Contains(got, summary) {
		t.Fatalf("missing summary: %q", got)
	}
	if !strings.Contains(got, "python3 -m pip install -r myapp/backend/requirements.txt") {
		t.Fatalf("missing profile pip path: %q", got)
	}
	if !strings.Contains(got, "myapp/backend/main.py") {
		t.Fatalf("missing stub path from summary: %q", got)
	}
	if !strings.Contains(got, "python3 -m pytest") {
		t.Fatalf("missing profile verify hint: %q", got)
	}
}
