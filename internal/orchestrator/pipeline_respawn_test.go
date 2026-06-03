package orchestrator

import "testing"

func TestRigNameFromPipelineSessionID(t *testing.T) {
	if got := rigNameFromPipelineSessionID("te-testgt3-polecat"); got != "testgt3" {
		t.Fatalf("polecat: got %q", got)
	}
	if got := rigNameFromPipelineSessionID("te-myapp-architect"); got != "myapp" {
		t.Fatalf("architect: got %q", got)
	}
	if got := rigNameFromPipelineSessionID("hq-mayor"); got != "" {
		t.Fatalf("mayor: got %q", got)
	}
}

func TestIsOrchestratedRigPipelineSession(t *testing.T) {
	if !isOrchestratedRigPipelineSession("te-testgt3-polecat") {
		t.Fatal("polecat should be orchestrated pipeline")
	}
	if isOrchestratedRigPipelineSession("te-testgt3-witness") {
		t.Fatal("witness is patrol, not orchestrated pipeline gate")
	}
}
