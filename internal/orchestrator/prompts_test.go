package orchestrator

import "testing"

func TestOrchestratedForRole(t *testing.T) {
	if OrchestratedForRole(true, "witness") {
		t.Fatal("witness must not be orchestrated")
	}
	if OrchestratedForRole(true, "refinery") {
		t.Fatal("refinery must not be orchestrated")
	}
	if OrchestratedForRole(true, "mechanic") {
		t.Fatal("mechanic must not be orchestrated")
	}
	if !OrchestratedForRole(true, "mayor") {
		t.Fatal("mayor should be orchestrated")
	}
	if !OrchestratedForTownPolecat(true) {
		t.Fatal("hq polecat should be orchestrated")
	}
	if OrchestratedForTownPolecat(false) {
		t.Fatal("hq polecat should not be orchestrated when service down")
	}
}

func TestAgentMatchesTask_edgeCases(t *testing.T) {
	vars := map[string]string{"rig": "mockrig"}
	if !AgentMatchesTask("any", "architect", vars) {
		t.Fatal("any should match")
	}
	if AgentMatchesTask("mockrig/qa", "architect", vars) {
		t.Fatal("wrong role suffix should not match")
	}
	if !AgentMatchesTask("mockrig/qa", "qa", vars) {
		t.Fatal("rig-qualified qa should match")
	}
	if AgentMatchesTask("qa", "qa", vars) {
		t.Fatal("bare qa should not match when workflow has rig")
	}
	if !AgentMatchesTask("mockrig/polecat", "polecat", vars) {
		t.Fatal("rig polecat should match")
	}
	if AgentMatchesTask("polecat", "polecat", vars) {
		t.Fatal("bare polecat should not match when workflow has rig")
	}
	if !AgentMatchesTask("planner", "planner", nil) {
		t.Fatal("town planner without rig var should match")
	}
	if AgentMatchesTask("planner", "setup", vars) {
		t.Fatal("planner must not claim project_setup (role setup)")
	}
	if !AgentMatchesTask("setup", "setup", vars) {
		t.Fatal("town setup agent should claim project_setup")
	}
}
