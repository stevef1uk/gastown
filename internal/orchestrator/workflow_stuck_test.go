package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEvalWorkflowStuck_phaseIdleNoBeadProgress(t *testing.T) {
	entered := time.Now().UTC().Add(-45 * time.Minute).Format(time.RFC3339)
	cfg := WorkflowStuckConfig{
		IdleAfter:    30 * time.Minute,
		StateGrace:   10 * time.Minute,
		ReworkLinger: 20 * time.Minute,
	}
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:                time.Now().UTC(),
		Config:             cfg,
		CurrentState:       "implementation",
		StateEnteredAt:     entered,
		BeadFingerprint:    "open:te-1:Implement linkshelf/go.mod",
		LastBeadFingerprint: "open:te-1:Implement linkshelf/go.mod",
		PolecatRunning:     true,
	})
	if !got.Stuck {
		t.Fatalf("expected stuck, got %#v", got)
	}
	if !containsStuckSignal(got.Signals, SignalPhaseIdleNoBeadProgress) {
		t.Fatalf("signals = %v", got.Signals)
	}
}

func TestEvalWorkflowStuck_pendingReworkLinger(t *testing.T) {
	entered := time.Now().UTC().Add(-25 * time.Minute).Format(time.RFC3339)
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:            time.Now().UTC(),
		Config:         WorkflowStuckConfig{ReworkLinger: 20 * time.Minute, StateGrace: 5 * time.Minute},
		CurrentState:   "implementation",
		StateEnteredAt: entered,
		PendingRework:  true,
		PolecatRunning: true,
	})
	if !containsStuckSignal(got.Signals, SignalPendingReworkLinger) {
		t.Fatalf("signals = %v", got.Signals)
	}
}

func TestEvalWorkflowStuck_polecatMissing(t *testing.T) {
	entered := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:            time.Now().UTC(),
		Config:         WorkflowStuckConfig{StateGrace: 5 * time.Minute},
		CurrentState:   "implementation",
		StateEnteredAt: entered,
		PolecatRunning: false,
	})
	if !containsStuckSignal(got.Signals, SignalPolecatSessionMissing) {
		t.Fatalf("signals = %v", got.Signals)
	}
}

func TestEvalWorkflowStuck_planningDocsMisaligned(t *testing.T) {
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:                    time.Now().UTC(),
		Config:                 WorkflowStuckConfig{StateGrace: time.Minute},
		CurrentState:           "planning",
		StateEnteredAt:         time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339),
		PlanningDocsMisaligned: true,
	})
	if !containsStuckSignal(got.Signals, SignalPlanningDocsMisaligned) {
		t.Fatalf("expected planning_docs_misaligned signal, got %v", got.Signals)
	}
}

func TestEvalWorkflowStuck_missingIntegrationContract(t *testing.T) {
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:                time.Now().UTC(),
		Config:             WorkflowStuckConfig{},
		CurrentState:       "planning",
		StateEnteredAt:     time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339),
		MissingIntegration: true,
	})
	if !containsStuckSignal(got.Signals, SignalMissingIntegrationContract) {
		t.Fatalf("signals = %v", got.Signals)
	}
}

func TestEvalWorkflowStuck_graceSuppressesEarlyIdle(t *testing.T) {
	entered := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)
	got := EvalWorkflowStuck(WorkflowStuckEvalInput{
		Now:                 time.Now().UTC(),
		Config:              WorkflowStuckConfig{IdleAfter: 30 * time.Minute, StateGrace: 20 * time.Minute},
		CurrentState:        "implementation",
		StateEnteredAt:      entered,
		BeadFingerprint:     "a",
		LastBeadFingerprint: "a",
		PolecatRunning:      true,
	})
	if got.Stuck {
		t.Fatalf("expected not stuck during grace, got %#v", got)
	}
}

func TestPlanMissingIntegrationContract(t *testing.T) {
	dir := t.TempDir()
	plan := "# Implementation plan\n\n## Bead map\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	if !PlanMissingIntegrationContract(dir, v) {
		t.Fatal("expected missing integration contract")
	}
}

func TestWorkflowStuckConfigFromEnv_disable(t *testing.T) {
	t.Setenv(envWorkflowStuckMonitor, "0")
	cfg := WorkflowStuckConfigFromEnv()
	if cfg.Enabled {
		t.Fatal("expected monitor disabled")
	}
}

func containsStuckSignal(signals []WorkflowStuckSignal, want WorkflowStuckSignal) bool {
	for _, s := range signals {
		if s == want {
			return true
		}
	}
	return false
}
