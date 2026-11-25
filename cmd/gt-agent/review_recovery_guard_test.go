package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestTryAutoReviewRecovery(t *testing.T) {
	tests := []struct {
		name        string
		fromState   string
		summary     string
		wantOutcome string
		wantAuto    bool
	}{
		{
			name:        "test_review failure with test repair",
			fromState:   "test_review",
			summary:     "Test repair: taskboard/internal/store/store_test.go — add missing contract tests",
			wantOutcome: "implementation_rework",
			wantAuto:    true,
		},
		{
			name:        "test_review failure with plan gap",
			fromState:   "test_review",
			summary:     "plan_gap: TEST_PLAN.md demands assertions for features not in SPEC",
			wantOutcome: "tester_rework",
			wantAuto:    true,
		},
		{
			name:        "test_review failure with spec contradiction",
			fromState:   "test_review",
			summary:     "spec contradiction: architecture.md contradicts SPEC §5",
			wantOutcome: "architect_rework",
			wantAuto:    true,
		},
		{
			name:        "plan_review failure",
			fromState:   "plan_review",
			summary:     "Missing required implementation beads in plan.md",
			wantOutcome: "planner_rework",
			wantAuto:    true,
		},
		{
			name:        "plan_review architecture failure",
			fromState:   "plan_review",
			summary:     "architecture drift in plan.md",
			wantOutcome: "architect_rework",
			wantAuto:    true,
		},
		{
			name:        "qa_review failure",
			fromState:   "qa_review",
			summary:     "Phase verify failed: go test ./... exit status 1",
			wantOutcome: "implementation_rework",
			wantAuto:    true,
		},
		{
			name:        "qa_review architecture contradiction",
			fromState:   "qa_review",
			summary:     "architecture contract violation: HTTP endpoints not matching SPEC",
			wantOutcome: "architect_rework",
			wantAuto:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &stateRunner{
				task: &orchestrator.Task{
					State: "review_recovery",
					PendingRework: &orchestrator.WorkflowRework{
						FromState: tt.fromState,
						Summary:   tt.summary,
					},
					AllowedOutcomes: []string{"implementation_rework", "tester_rework", "planner_rework", "architect_rework", "pause"},
				},
			}
			outcome, _, auto := runner.tryAutoOutcome()
			if auto != tt.wantAuto {
				t.Fatalf("tryAutoOutcome() auto = %v, want %v", auto, tt.wantAuto)
			}
			if outcome != tt.wantOutcome {
				t.Fatalf("tryAutoOutcome() outcome = %q, want %q", outcome, tt.wantOutcome)
			}
		})
	}

	t.Run("require explicit outcome disables auto recovery", func(t *testing.T) {
		runner := &stateRunner{
			hooks: orchestrator.StateHooks{
				RequireExplicitOutcome: true,
			},
			task: &orchestrator.Task{
				State: "review_recovery",
				PendingRework: &orchestrator.WorkflowRework{
					FromState: "qa_review",
					Summary:   "Phase verify failed",
				},
				AllowedOutcomes: []string{"implementation_rework", "pause"},
			},
		}
		if _, _, auto := runner.tryAutoOutcome(); auto {
			t.Fatal("tryAutoOutcome() should return auto=false when RequireExplicitOutcome=true")
		}
	})
}

func TestRejectReviewRecoverySpuriousPause(t *testing.T) {
	runner := &stateRunner{
		task: &orchestrator.Task{
			State: "review_recovery",
			PendingRework: &orchestrator.WorkflowRework{
				FromState: "test_review",
				Summary:   "Missing tests in store_test.go",
			},
		},
	}

	spuriousSummaries := []string{
		"Inspection blocked: tool did not return CMD output; cannot proceed without reading",
		"cat command failed with exit status 1",
		"no such file or directory while inspecting test files",
		"tool timed out waiting for shell output",
	}

	for _, s := range spuriousSummaries {
		msg, reject := runner.rejectReviewRecoverySpuriousPause("pause", s)
		if !reject {
			t.Errorf("expected reject for spurious summary: %q", s)
		}
		if msg == "" {
			t.Errorf("expected non-empty rejection message for: %q", s)
		}
	}

	genuineSummary := "Unresolvable requirements contradiction: user prompt demands PostgreSQL 16 only but SPEC demands SQLite default"
	if _, reject := runner.rejectReviewRecoverySpuriousPause("pause", genuineSummary); reject {
		t.Errorf("unexpected reject for genuine requirement conflict")
	}
}

func TestCdPrefixMayorRigNormalization(t *testing.T) {
	rig := "tasktracker"
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "cat tasktracker/taskboard/internal/store/store_test.go",
			want:  "cd tasktracker/mayor/rig && cat taskboard/internal/store/store_test.go",
		},
		{
			input: "cat ../../taskboard/internal/store/store_test.go",
			want:  "cd tasktracker/mayor/rig && cat taskboard/internal/store/store_test.go",
		},
		{
			input: "find tasktracker -name 'TEST_PLAN.md'",
			want:  "cd tasktracker/mayor/rig && find . -name 'TEST_PLAN.md'",
		},
	}

	for _, tt := range tests {
		got, ok := cdPrefixMayorRig(tt.input, rig)
		if !ok {
			t.Errorf("cdPrefixMayorRig(%q) ok = false, want true", tt.input)
		}
		if got != tt.want {
			t.Errorf("cdPrefixMayorRig(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
