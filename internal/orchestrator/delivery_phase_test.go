package orchestrator

import "testing"

func TestFinalizeDeliveryPhases_unionAndDefaultActive(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "p1", RequiredFiles: []string{"app/a.go"}},
			{ID: "p2", RequiredFiles: []string{"app/b.go", "app/a.go"}},
		},
	}
	got := FinalizeDeliveryPhases(v)
	if got.ActivePhaseIDField != "p1" {
		t.Fatalf("active_phase_id = %q, want p1", got.ActivePhaseIDField)
	}
	if len(got.RequiredFiles) != 2 {
		t.Fatalf("union required_files len = %d, want 2", len(got.RequiredFiles))
	}
}

func TestForActivePhase_scopesFilesAndQA(t *testing.T) {
	v := WorkflowValidation{
		RequiredFiles:      []string{"a.go", "b.go", "c.go"},
		QAVerifyCommand:    "pytest -q",
		ActivePhaseIDField: "p2",
		DeliveryPhases: []DeliveryPhase{
			{ID: "p1", RequiredFiles: []string{"a.go"}, QAVerifyCommand: "go test ./p1/..."},
			{ID: "p2", RequiredFiles: []string{"b.go"}, QAVerifyCommand: "npm test"},
		},
	}
	scoped := v.ForActivePhase()
	if len(scoped.RequiredFiles) != 1 || scoped.RequiredFiles[0] != "b.go" {
		t.Fatalf("scoped files = %v", scoped.RequiredFiles)
	}
	if scoped.QAVerifyCommand != "npm test" {
		t.Fatalf("qa = %q", scoped.QAVerifyCommand)
	}
}

func TestValidatePlanBeads_activePhaseOnly(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains:  "Implement ",
		ActivePhaseIDField: "p1",
		RequiredFiles:      []string{"a.go", "b.go"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "p1", RequiredFiles: []string{"a.go"}},
			{ID: "p2", RequiredFiles: []string{"b.go"}},
		},
	}
	beads := []PlanBead{{ID: "te-1", Title: "Implement a.go per architecture"}}
	if err := ValidatePlanBeads(beads, "", v); err != nil {
		t.Fatalf("expected ok for single phase bead: %v", err)
	}
	beads = append(beads, PlanBead{ID: "te-2", Title: "Implement b.go per architecture"})
	if err := ValidatePlanBeads(beads, "", v); err == nil {
		t.Fatal("expected error when extra bead for future phase path")
	}
}
