package orchestrator

import (
	"testing"
)

func TestEffectiveStateTimeoutSeconds_envOverride(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "1800")
	h := StateHooks{StateTimeoutSeconds: 7200}
	if got := h.EffectiveStateTimeoutSeconds(); got != 1800 {
		t.Fatalf("got %d, want 1800 from env", got)
	}
}

func TestEffectiveStateTimeoutSeconds_yamlDefault(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "")
	h := StateHooks{StateTimeoutSeconds: 7200}
	if got := h.EffectiveStateTimeoutSeconds(); got != 7200 {
		t.Fatalf("got %d, want 7200", got)
	}
}

func TestEffectiveStateTimeoutSeconds_invalidEnvIgnored(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "nope")
	h := StateHooks{StateTimeoutSeconds: 900}
	if got := h.EffectiveStateTimeoutSeconds(); got != 900 {
		t.Fatalf("got %d, want 900", got)
	}
}
