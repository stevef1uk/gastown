package orchestrator

import (
	"strings"
	"testing"
)

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

func TestFinalizeDeliveryPhases_collapseAndSplit(t *testing.T) {
	v := WorkflowValidation{
		ActivePhaseIDField: "e2e",
		DeliveryPhases: []DeliveryPhase{
			{ID: "e2e-1", Title: "E2E (part 1/2)", RequiredFiles: []string{"a.sh", "b.sh", "c.sh", "d.sh", "e.sh", "f.sh", "g.sh", "h.sh", "i.sh", "j.sh", "k.sh"}},
			{ID: "e2e-2", Title: "E2E (part 2/2)", RequiredFiles: []string{"l.sh", "m.sh"}},
		},
	}
	got := FinalizeDeliveryPhases(v)
	if len(got.DeliveryPhases) != 2 {
		t.Fatalf("phases = %d, want 2 (collapsed then split once): %v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	if got.DeliveryPhases[0].ID != "e2e-1" || got.DeliveryPhases[1].ID != "e2e-2" {
		t.Fatalf("phase IDs = %v, want e2e-1, e2e-2", []string{got.DeliveryPhases[0].ID, got.DeliveryPhases[1].ID})
	}
	if strings.Contains(got.DeliveryPhases[0].Title, "(part 1/2)") && strings.Contains(got.DeliveryPhases[0].Title, "(part 2/2)") {
		t.Fatalf("title still nested: %q", got.DeliveryPhases[0].Title)
	}
	if got.ActivePhaseIDField != "e2e-1" {
		t.Fatalf("active_phase_id = %q, want e2e-1", got.ActivePhaseIDField)
	}
}

func TestFinalizeDeliveryPhases_activePhaseMapsToSplit(t *testing.T) {
	v := WorkflowValidation{
		ActivePhaseIDField: "e2e",
		DeliveryPhases: []DeliveryPhase{
			{ID: "backend", RequiredFiles: []string{"main.py"}},
			{ID: "e2e", RequiredFiles: []string{
				"a.sh", "b.sh", "c.sh", "d.sh", "e.sh",
				"f.sh", "g.sh", "h.sh", "i.sh", "j.sh", "k.sh",
			}},
		},
	}
	got := FinalizeDeliveryPhases(v)
	if got.ActivePhaseIDField != "e2e-1" {
		t.Fatalf("active_phase_id = %q, want e2e-1", got.ActivePhaseIDField)
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

func TestForActivePhase_webStaticKeepsGoTest(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		ActivePhaseIDField: "web-static",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:            "web-static",
				RequiredFiles: []string{"linkshelf/web/style.css", "linkshelf/web/app.js"},
			},
		},
	}
	scoped := v.ForActivePhase()
	want := "cd linkshelf && go test ./..."
	if scoped.QAVerifyCommand != want {
		t.Fatalf("qa = %q want %q", scoped.QAVerifyCommand, want)
	}
}

func TestPhaseIsGoModOnly(t *testing.T) {
	if !PhaseIsGoModOnly(WorkflowValidation{RequiredFiles: []string{"linkshelf/go.mod"}}) {
		t.Fatal("go.mod only")
	}
	if PhaseIsGoModOnly(WorkflowValidation{RequiredFiles: []string{"linkshelf/web/app.js"}}) {
		t.Fatal("frontend only")
	}
	if PhaseIsGoModOnly(WorkflowValidation{RequiredFiles: nil}) {
		t.Fatal("empty required_files")
	}
}

func TestForActivePhase_goModuleUsesDownload(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		ActivePhaseIDField: "go-module",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "go-module",
				RequiredFiles:   []string{"linkshelf/go.mod"},
				QAVerifyCommand: "cd linkshelf && go mod tidy",
			},
			{ID: "store-layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
		},
	}
	scoped := v.ForActivePhase()
	want := "cd linkshelf && go mod download"
	if scoped.QAVerifyCommand != want {
		t.Fatalf("qa = %q want %q", scoped.QAVerifyCommand, want)
	}
	if vars := scoped.PromptVars()["unittest_command_hint"]; vars != want {
		t.Fatalf("hint = %q want %q", vars, want)
	}
}

func TestPairPhaseInfraFiles_addsNodeFilesToFrontendPhase(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "cd app && npm test",
		ActivePhaseIDField: "frontend-ui",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "backend-api",
				RequiredFiles: []string{"app/backend/api.py"},
			},
			{
				ID:    "frontend-ui",
				RequiredFiles: []string{
					"app/frontend/components/Chart.tsx",
					"app/frontend/__tests__/Chart.test.tsx",
				},
			},
		},
	}
	got := FinalizeDeliveryPhases(v)
	var frontendPhase *DeliveryPhase
	for i := range got.DeliveryPhases {
		if got.DeliveryPhases[i].ID == "frontend-ui" {
			frontendPhase = &got.DeliveryPhases[i]
			break
		}
	}
	if frontendPhase == nil {
		t.Fatal("frontend-ui phase not found")
	}
	hasPackageJSON := false
	hasTsconfig := false
	for _, f := range frontendPhase.RequiredFiles {
		if f == "app/frontend/package.json" {
			hasPackageJSON = true
		}
		if f == "app/frontend/tsconfig.json" {
			hasTsconfig = true
		}
	}
	if !hasPackageJSON {
		t.Fatal("frontend-ui missing app/frontend/package.json")
	}
	if !hasTsconfig {
		t.Fatal("frontend-ui missing app/frontend/tsconfig.json")
	}
	hasUnionPackageJSON := false
	hasUnionTsconfig := false
	for _, f := range got.RequiredFiles {
		if f == "app/frontend/package.json" {
			hasUnionPackageJSON = true
		}
		if f == "app/frontend/tsconfig.json" {
			hasUnionTsconfig = true
		}
	}
	if !hasUnionPackageJSON {
		t.Fatal("union required_files missing app/frontend/package.json")
	}
	if !hasUnionTsconfig {
		t.Fatal("union required_files missing app/frontend/tsconfig.json")
	}
	// Backend phase should not get Node infra files
	backendPhase := got.DeliveryPhases[0]
	for _, f := range backendPhase.RequiredFiles {
		if strings.HasSuffix(f, "package.json") || strings.HasSuffix(f, "tsconfig.json") {
			t.Fatalf("backend phase got infra file %q", f)
		}
	}
}

func TestPairPhaseInfraFiles_skipsNonNodePhases(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "cd app && go test ./...",
		ActivePhaseIDField: "backend",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "backend",
				RequiredFiles: []string{"app/main.go", "app/handler.go"},
			},
		},
	}
	got := FinalizeDeliveryPhases(v)
	phase := got.DeliveryPhases[0]
	for _, f := range phase.RequiredFiles {
		if f == "app/package.json" || f == "app/tsconfig.json" {
			t.Fatalf("Go phase should not get Node infra files, got %q", f)
		}
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

func TestFinalizeDeliveryPhases_noRootPackageJsonForTestDir(t *testing.T) {
	v := WorkflowValidation{
		RequiredFiles: []string{
			"frontend/app/page.tsx",
			"test/playwright.config.ts",
			"test/e2e/trading_flow.spec.ts",
		},
		DeliveryPhases: []DeliveryPhase{
			{ID: "frontend-ui", RequiredFiles: []string{
				"frontend/services/api.ts",
				"frontend/app/page.tsx",
			}},
			{ID: "e2e-and-deployment", RequiredFiles: []string{
				"test/package.json",
				"test/tsconfig.json",
				"test/e2e/trading_flow.spec.ts",
				"test/playwright.config.ts",
			}},
		},
	}
	got := FinalizeDeliveryPhases(v)
	for _, p := range got.DeliveryPhases {
		for _, f := range p.RequiredFiles {
			if f == "package.json" || f == "tsconfig.json" {
				t.Fatalf("phase %q must not contain root %q", p.ID, f)
			}
		}
	}
	for _, f := range got.RequiredFiles {
		if f == "package.json" || f == "tsconfig.json" {
			t.Fatalf("top-level required_files must not contain %q", f)
		}
	}
	// Ensure parent manifests are still inferred when the source is in a subdir.
	wantFrontend := map[string]bool{"frontend/package.json": true, "frontend/tsconfig.json": true}
	for _, p := range got.DeliveryPhases {
		if p.ID != "frontend-ui" {
			continue
		}
		for _, f := range p.RequiredFiles {
			delete(wantFrontend, f)
		}
	}
	if len(wantFrontend) > 0 {
		t.Fatalf("frontend-ui missing inferred manifests: %v", wantFrontend)
	}
}

func TestFinalizeDeliveryPhases_dockerVerifyFollowsDockerFiles(t *testing.T) {
	// Simulate a single E2E phase that gets split; its Docker-based verify command
	// should follow the Docker files into the final sub-phase.
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "e2e-and-deployment", RequiredFiles: []string{
				"test/package.json",
				"test/tsconfig.json",
				"test/e2e/trading_flow.spec.ts",
				".env.example",
				"scripts/start_mac.sh",
				"scripts/stop_mac.sh",
				"scripts/start_windows.ps1",
				"scripts/stop_windows.ps1",
				"test/docker-compose.test.yml",
				"Dockerfile",
				"docker-compose.yml",
			}, QAVerifyCommand: "cd test && docker-compose -f docker-compose.test.yml up --exit-code-from playwright"},
		},
	}
	got := FinalizeDeliveryPhases(v)
	if len(got.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	var phase1, phase2 *DeliveryPhase
	for i := range got.DeliveryPhases {
		p := &got.DeliveryPhases[i]
		if strings.HasPrefix(p.ID, "e2e-and-deployment-1") {
			phase1 = p
		}
		if strings.HasPrefix(p.ID, "e2e-and-deployment-2") {
			phase2 = p
		}
	}
	if phase1 == nil || phase2 == nil {
		t.Fatalf("missing phases: got %+v", got.DeliveryPhases)
	}
	for _, f := range []string{"Dockerfile", "test/docker-compose.test.yml", "docker-compose.yml"} {
		for _, pf := range phase1.RequiredFiles {
			if pf == f {
				t.Fatalf("phase 1 still contains docker file %q", f)
			}
		}
	}
	if qaVerifyCommandReferencesDocker(phase1.QAVerifyCommand) {
		t.Fatalf("phase 1 QA command still references docker: %q", phase1.QAVerifyCommand)
	}
	if phase2.QAVerifyCommand != "cd test && docker-compose -f docker-compose.test.yml up --exit-code-from playwright" {
		t.Fatalf("phase 2 QA command = %q, want docker compose command", phase2.QAVerifyCommand)
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

func TestFinalizeDeliveryPhases_setupPhaseFirst(t *testing.T) {
	// Directory-grouped fallback (PhasesFromFilePaths) sorts keys alphabetically, so
	// the root/setup phase (go.mod) can land mid-list. It must run before any source
	// phase and become the active phase.
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		DeliveryPhases: []DeliveryPhase{
			{ID: "cmd", Title: "Cmd Layer", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
			{ID: "internal", Title: "Internal Layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
			{ID: "setup", Title: "Setup and Root Files", RequiredFiles: []string{"linkshelf/go.mod"}},
			{ID: "web", Title: "Web Layer", RequiredFiles: []string{"linkshelf/web/index.html"}},
		},
		ActivePhaseIDField: "cmd",
	}
	got := FinalizeDeliveryPhases(v)
	if len(got.DeliveryPhases) != 4 || got.DeliveryPhases[0].ID != "setup" {
		t.Fatalf("setup phase must be first, got %v", phaseIDs(got.DeliveryPhases))
	}
	if got.ActivePhaseID() != "setup" {
		t.Fatalf("active_phase_id = %q, want setup", got.ActivePhaseID())
	}
	// go.mod must appear in the first (active) phase's required files.
	if active, ok := got.ActivePhase(); !ok || !containsString(active.RequiredFiles, "linkshelf/go.mod") {
		t.Fatalf("active phase must include go.mod, got %v", active.RequiredFiles)
	}
}

func TestFinalizeDeliveryPhases_setupPhaseFirst_specOrder(t *testing.T) {
	// SPEC-derived phases name the module phase "go-module"; it already leads but must
	// stay first and stay the active phase.
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", Title: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
			{ID: "store-layer", Title: "store-layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
			{ID: "api-handlers", Title: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handler.go"}},
			{ID: "server-main", Title: "server-main", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
		},
		ActivePhaseIDField: "go-module",
	}
	got := FinalizeDeliveryPhases(v)
	if got.DeliveryPhases[0].ID != "go-module" {
		t.Fatalf("go-module phase must stay first, got %v", phaseIDs(got.DeliveryPhases))
	}
	if got.ActivePhaseID() != "go-module" {
		t.Fatalf("active_phase_id = %q, want go-module", got.ActivePhaseID())
	}
}

func TestFinalizeDeliveryPhases_setupPhaseFirst_mixedRootNotMoved(t *testing.T) {
	// A phase mixing a root config file with source (go.mod + main.go) is NOT a pure
	// setup phase and must not be hoisted.
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{ID: "core", Title: "Core", RequiredFiles: []string{"app/main.go", "app/go.mod"}},
			{ID: "web", Title: "Web", RequiredFiles: []string{"app/web/index.html"}},
		},
		ActivePhaseIDField: "core",
	}
	got := FinalizeDeliveryPhases(v)
	if len(got.DeliveryPhases) != 2 || got.DeliveryPhases[0].ID != "core" {
		t.Fatalf("mixed-root phase must not be hoisted, got %v", phaseIDs(got.DeliveryPhases))
	}
}

func TestFinalizeDeliveryPhases_setupPhaseFirst_completedPreserved(t *testing.T) {
	// Once phases are completed, active_phase_id must not be rewound to the setup phase.
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		CompletedPhaseIDsField: []string{"setup"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "cmd", Title: "Cmd Layer", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
			{ID: "setup", Title: "Setup and Root Files", RequiredFiles: []string{"linkshelf/go.mod"}},
		},
		ActivePhaseIDField: "cmd",
	}
	got := FinalizeDeliveryPhases(v)
	if got.DeliveryPhases[0].ID != "setup" {
		t.Fatalf("setup phase must still be hoisted, got %v", phaseIDs(got.DeliveryPhases))
	}
	if got.ActivePhaseID() != "cmd" {
		t.Fatalf("active_phase_id = %q, want cmd preserved after completion", got.ActivePhaseID())
	}
}
