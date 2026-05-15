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
