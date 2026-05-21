package main

import (
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestOrchestratedCommandTimeoutForTrack_qaSmokeShorterThanPolecat(t *testing.T) {
	cmd := "cd rig/mayor/rig/app && go run ./cmd/server & sleep 2 && curl -sf http://127.0.0.1:8080/"
	qa := orchestratedCommandTimeoutForTrack("qa", cmd)
	polecat := orchestratedCommandTimeoutForTrack("implementation", cmd)
	if qa != 90*time.Second {
		t.Fatalf("qa smoke timeout = %v, want 90s", qa)
	}
	if polecat != 3*time.Minute {
		t.Fatalf("polecat smoke timeout = %v, want 3m", polecat)
	}
}

func TestStateRunner_effectiveCommandTimeoutSec_qaUsesTrackDefault(t *testing.T) {
	task := &orchestrator.Task{
		State: "qa_review",
		Hooks: orchestrator.StateHooks{Track: "qa"},
	}
	r := newStateRunner(task, t.TempDir(), "testrig")
	cmd := "go run ./cmd/server & curl -sf http://127.0.0.1:8080/api/x"
	if got := r.effectiveCommandTimeoutSec(cmd); got != 90 {
		t.Fatalf("effective timeout = %d, want 90", got)
	}
}

func TestStateRunner_effectiveCommandTimeoutSec_yamlOverrideWins(t *testing.T) {
	task := &orchestrator.Task{
		State: "qa_review",
		Hooks: orchestrator.StateHooks{Track: "qa", CmdTimeoutSeconds: 45},
	}
	r := newStateRunner(task, t.TempDir(), "testrig")
	cmd := "go run ./cmd/server & curl -sf http://127.0.0.1:8080/"
	if got := r.effectiveCommandTimeoutSec(cmd); got != 45 {
		t.Fatalf("effective timeout = %d, want 45 from yaml", got)
	}
}

func TestAppendQAFailureReportNudge_includesFailureJSON(t *testing.T) {
	var b strings.Builder
	appendQAFailureReportNudge(&b, "go run ./cmd/server & curl -sf http://127.0.0.1:8080/", errQACommandTimeout)
	out := b.String()
	if !strings.Contains(out, `"outcome":"failure"`) {
		t.Fatalf("nudge should suggest failure JSON: %q", out)
	}
	if !strings.Contains(out, "Do **not** repeat") {
		t.Fatalf("nudge should discourage retry: %q", out)
	}
}

var errQACommandTimeout = &timeoutError{msg: "command exceeded 90s"}

type timeoutError struct{ msg string }

func (e *timeoutError) Error() string { return e.msg }
