package orchestrator

import (
	"strings"
	"testing"
)

func TestPrepareWorkflowReworkFeedback_qaToDesign(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	got := PrepareWorkflowReworkFeedback("qa_review", "design", "smoke POST 405", "curl: (22) HTTP 405\n---\ngo test ./... ok", "", v, map[string]string{"qa_review->design": "qa_review_to_design"})
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

func TestPrepareWorkflowReworkFeedback_contractMismatchRoute(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "demo", QAVerifyCommand: "cd demo && pytest"}
	summary := `Implementation repair: demo/tests/test_api.py — SPEC: SPEC.md §API-3 '400 {"error":...}'; Assertion: demo/tests/test_api.py:115 'assert detail'; Production fix: demo/main.py:179 return error envelope; reopen demo-api`
	routes := map[string]string{
		"test_review->implementation":                   "test_review_to_implementation",
		"test_review->implementation:contract_mismatch": "test_review_contract_mismatch_to_implementation",
	}
	got := PrepareWorkflowReworkFeedback("test_review", "implementation", summary, "", "contract_mismatch", v, routes)
	for _, want := range []string{"SPEC-conformance mismatch", "do not weaken or delete tests", "Production fix:", "Polecat recovery steps (contract_mismatch)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in feedback: %s", want, got)
		}
	}
	if strings.Contains(got, "missing or weak tests") {
		t.Fatalf("generic test-repair guidance leaked into contract mismatch: %s", got)
	}
}

func TestPrepareWorkflowReworkFeedback_contractMismatchFallsBackToTransitionRoute(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "demo"}
	routes := map[string]string{"test_review->implementation": "test_review_to_implementation"}
	got := PrepareWorkflowReworkFeedback("test_review", "implementation", "Implementation repair: demo/tests/test_api.py", "", "contract_mismatch", v, routes)
	if !strings.Contains(got, "missing or weak tests") {
		t.Fatalf("expected generic transition feedback when no outcome route exists: %s", got)
	}
}
