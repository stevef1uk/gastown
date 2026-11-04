package orchestrator

import (
	"strings"
	"testing"
)

func TestStateHooks_EffectiveMaxCmdTurns(t *testing.T) {
	t.Parallel()
	h := StateHooks{}
	if h.EffectiveMaxCmdTurns() != 5 {
		t.Fatalf("default = %d", h.EffectiveMaxCmdTurns())
	}
	h.MaxCmdTurns = 8
	if h.EffectiveMaxCmdTurns() != 8 {
		t.Fatal("expected 8")
	}
}

func TestStateHooks_RequireExplicitOutcomeOverride(t *testing.T) {
	h := StateHooks{}.ApplyOverrides(StateHooks{RequireExplicitOutcome: true})
	if !h.RequireExplicitOutcome || !h.ApplyOverrides(StateHooks{}).RequireExplicitOutcome {
		t.Fatal("explicit review verdict requirement must survive hook overrides")
	}
}

func TestRetryHintKey_project_setup(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{QAVerifyCommand: "cd linkshelf && go test ./..."}
	h := StateHooks{RetryHintKey: "project_setup"}
	if !strings.Contains(h.RetryHintText(v, nil), "go mod") {
		t.Fatal("expected Go setup hint")
	}
}
