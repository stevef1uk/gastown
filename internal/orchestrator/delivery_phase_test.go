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

func TestMoveDockerPathsToFinalDeliveryPhase(t *testing.T) {
	v := WorkflowValidation{
		ActivePhaseIDField: "setup-infrastructure",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:            "setup-infrastructure",
				Title:         "Setup Infrastructure",
				RequiredFiles: []string{"Dockerfile", "docker-compose.yml", "backend/main.py"},
			},
			{
				ID:            "backend-core",
				RequiredFiles: []string{"backend/db/schema.sql"},
			},
		},
	}
	got := FinalizeDeliveryPhases(v)
	if got.ActivePhaseIDField != "setup-infrastructure" {
		t.Fatalf("active_phase_id = %q, want setup-infrastructure", got.ActivePhaseIDField)
	}
	first := got.DeliveryPhases[0]
	if len(first.RequiredFiles) != 1 || first.RequiredFiles[0] != "backend/main.py" {
		t.Fatalf("first phase files = %v, want only backend/main.py", first.RequiredFiles)
	}
	last := got.DeliveryPhases[len(got.DeliveryPhases)-1]
	wantLast := []string{"backend/db/schema.sql", "Dockerfile", "docker-compose.yml"}
	if len(last.RequiredFiles) != len(wantLast) {
		t.Fatalf("last.RequiredFiles = %v, want %v", last.RequiredFiles, wantLast)
	}
	for i, p := range wantLast {
		if last.RequiredFiles[i] != p {
			t.Fatalf("last.RequiredFiles = %v, want %v", last.RequiredFiles, wantLast)
		}
	}
}

func TestNextDeliveryPhaseID(t *testing.T) {
	v := WorkflowValidation{
		ActivePhaseIDField: "p1",
		DeliveryPhases: []DeliveryPhase{
			{ID: "p1"},
			{ID: "p2"},
			{ID: "p3"},
		},
	}
	next, ok := v.NextDeliveryPhaseID()
	if !ok || next != "p2" {
		t.Fatalf("got %q ok=%v want p2", next, ok)
	}
	v.ActivePhaseIDField = "p3"
	if _, ok := v.NextDeliveryPhaseID(); ok {
		t.Fatal("expected no next after last phase")
	}
}

func TestRequiredFilesForSmokeScope_phasedUsesActiveOnly(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		ActivePhaseIDField: "backend-core",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/index.html",
		},
		DeliveryPhases: []DeliveryPhase{
			{ID: "backend-core", RequiredFiles: []string{
				"linkshelf/go.mod",
				"linkshelf/internal/store/schema.go",
				"linkshelf/internal/store/store.go",
			}},
			{ID: "server-setup", RequiredFiles: []string{
				"linkshelf/cmd/server/main.go",
				"linkshelf/web/index.html",
			}},
		},
	}
	got := v.RequiredFilesForSmokeScope()
	if len(got) != 3 {
		t.Fatalf("smoke scope = %v, want 3 store paths", got)
	}
	if workflowHasGoWebAndServer(v) {
		t.Fatal("backend-core phase must not require web+server smoke")
	}
	if workflowHasGoWebAndServer(v.ForActivePhase()) {
		t.Fatal("scoped backend-core must not require web+server smoke")
	}
	v.ActivePhaseIDField = "server-setup"
	if !workflowHasGoWebAndServer(v) {
		t.Fatal("server-setup phase should require web+server smoke")
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
	if err := ValidatePlanBeads(beads, "", v, ""); err != nil {
		t.Fatalf("expected ok for single phase bead: %v", err)
	}
	beads = append(beads, PlanBead{ID: "te-2", Title: "Implement b.go per architecture"})
	if err := ValidatePlanBeads(beads, "", v, ""); err == nil {
		t.Fatal("expected error when extra bead for future phase path")
	}
}
