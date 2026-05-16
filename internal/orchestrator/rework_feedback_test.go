package orchestrator

import (
	"strings"
	"testing"
)

func TestPrepareWorkflowReworkFeedback_planReviewStripsGrepNoise(t *testing.T) {
	summary := "duplicate backend/main.py (te-a, te-b); plan.md size 1020 bytes (≥1000) ok"
	raw := "Command: grep -E te-a|te-b beads.md\nError: exit status 2\nOutput: grep: beads.md: No such file\n" +
		"grep: te-a: No such file\n" +
		"○ te-a [● P2] [task] - Implement backend/main.py per architecture\n" +
		"○ te-b [● P2] [task] - Implement backend/main.py per architecture\n" +
		"Use CMD: lines to run shell commands\n"

	got := PrepareWorkflowReworkFeedback("plan_review", "planning", summary, raw)
	if strings.Contains(got, "grep:") {
		t.Fatalf("expected grep noise stripped, got %q", got)
	}
	if !strings.Contains(got, summary) {
		t.Fatalf("expected summary preserved, got %q", got)
	}
	if !strings.Contains(got, "te-a") || !strings.Contains(got, "Implement backend/main.py") {
		t.Fatalf("expected bd list excerpt, got %q", got)
	}
	if !strings.Contains(got, "do not pad plan.md") {
		t.Fatalf("expected plan-ok guidance, got %q", got)
	}
}

func TestPlanReviewSummarySaysPlanOK(t *testing.T) {
	if !PlanReviewSummarySaysPlanOK("plan.md size 1020 bytes (≥1000) ok") {
		t.Fatal("expected ok")
	}
	if PlanReviewSummarySaysPlanOK("plan.md too small") {
		t.Fatal("expected false")
	}
}

func TestSanitizeAttemptFeedback(t *testing.T) {
	raw := "grep: foo: No such file\n○ te-x [task] - Implement backend/x.py\nUse CMD: hint\n"
	got := sanitizeAttemptFeedback(raw)
	if strings.Contains(got, "grep:") || strings.Contains(got, "Use CMD:") {
		t.Fatalf("unexpected noise: %q", got)
	}
	if !strings.Contains(got, "te-x") {
		t.Fatalf("expected bead line: %q", got)
	}
}
