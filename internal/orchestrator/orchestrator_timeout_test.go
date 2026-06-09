package orchestrator

import (
	"testing"
	"time"
)

func TestOrchestratorWorkflowCallTimeout_default(t *testing.T) {
	t.Setenv(OrchestratorNATSTimeoutEnv, "")
	if got := orchestratorWorkflowCallTimeout(); got != OrchestratorWorkflowCallTimeout {
		t.Fatalf("got %v want %v", got, OrchestratorWorkflowCallTimeout)
	}
}

func TestOrchestratorWorkflowCallTimeout_envOverride(t *testing.T) {
	t.Setenv(OrchestratorNATSTimeoutEnv, "90s")
	if got := orchestratorWorkflowCallTimeout(); got != 90*time.Second {
		t.Fatalf("got %v want 90s", got)
	}
}
