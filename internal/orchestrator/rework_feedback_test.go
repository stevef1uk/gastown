package orchestrator

import (
	"strings"
	"testing"
)

func TestPrepareWorkflowReworkFeedback_qaToDesign(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	got := PrepareWorkflowReworkFeedback("qa_review", "design", "smoke POST 405", "curl: (22) HTTP 405\n---\ngo test ./... ok", v, map[string]string{"qa_review->design": "qa_review_to_design"})
	if !strings.Contains(got, "architecture rework") {
		t.Fatalf("missing escalation preamble: %s", got)
	}
	if !strings.Contains(got, "smoke POST 405") {
		t.Fatalf("missing summary: %s", got)
	}
	if !strings.Contains(got, "architecture.md") {
		t.Fatalf("missing architect steps: %s", got)
	}
}
