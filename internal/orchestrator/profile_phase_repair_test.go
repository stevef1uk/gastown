package orchestrator

import (
	"strings"
	"testing"
)

// TestPhasesFromFilePaths_flatLayoutSinglePhase is the pingapp regression: a flat
// 3-file project must collapse into ONE phase, not requirementstxt/mainpy/test-mainpy.
func TestPhasesFromFilePaths_flatLayoutSinglePhase(t *testing.T) {
	paths := []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"}
	phases := PhasesFromFilePaths(paths)
	if len(phases) != 1 {
		t.Fatalf("flat layout must collapse to 1 phase, got %d: %+v", len(phases), phases)
	}
	got := make(map[string]bool, len(phases[0].RequiredFiles))
	for _, f := range phases[0].RequiredFiles {
		got[f] = true
	}
	for _, want := range paths {
		if !got[want] {
			t.Fatalf("phase %q missing file %q: %+v", phases[0].ID, want, phases[0].RequiredFiles)
		}
	}
	if phases[0].ID == "" {
		t.Fatal("flat phase must have an ID")
	}
}

// TestPhasesFromFilePaths_subdirectoryGrouping keeps one phase per real subdir and
// puts layout-root files in the setup phase.
func TestPhasesFromFilePaths_subdirectoryGrouping(t *testing.T) {
	paths := []string{
		"linkshelf/go.mod",
		"linkshelf/main.go",
		"linkshelf/cmd/server/main.go",
		"linkshelf/web/index.html",
		"linkshelf/internal/store/store.go",
	}
	phases := PhasesFromFilePaths(paths)
	if len(phases) < 4 {
		t.Fatalf("expected setup+cmd+web+internal phases, got %d: %+v", len(phases), phases)
	}
	hasSetup := false
	for _, p := range phases {
		if p.ID == "setup" {
			hasSetup = true
		}
	}
	if !hasSetup {
		t.Fatalf("expected a setup phase for root-level files: %+v", phases)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_removesGoFromPythonPhase is the pingapp
// QA regression: a Python phase must never keep go test/vet/run.
func TestSanitizePhaseVerifyCommandsForStack_removesGoFromPythonPhase(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"pingapp/main.py", "pingapp/test_main.py", "pingapp/requirements.txt"},
				QAVerifyCommand: "cd pingapp && go vet ./...",
			},
			{
				ID:              "config",
				RequiredFiles:   []string{"pingapp/requirements.txt"},
				QAVerifyCommand: "cd pingapp && go vet ./...",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	for _, p := range got.DeliveryPhases {
		lower := strings.ToLower(p.QAVerifyCommand)
		if strings.Contains(lower, "go vet") || strings.Contains(lower, "go test") || strings.Contains(lower, "go run") {
			t.Fatalf("phase %q kept a Go command in a Python rig: %q", p.ID, p.QAVerifyCommand)
		}
		if p.ID == "core" && !strings.Contains(lower, "pytest") {
			t.Fatalf("core python phase should verify with pytest, got %q", p.QAVerifyCommand)
		}
	}
}

// TestSanitizePhaseVerifyCommandsForStack_keepsGoCommand confirms the sanitizer does
// not rewrite legitimate Go phase commands.
func TestSanitizePhaseVerifyCommandsForStack_keepsGoCommand(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "backend-core",
				RequiredFiles:   []string{"linkshelf/cmd/server/main.go"},
				QAVerifyCommand: "cd linkshelf && go test ./...",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	if got.DeliveryPhases[0].QAVerifyCommand != "cd linkshelf && go test ./..." {
		t.Fatalf("legit Go command was rewritten: %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_nodePhaseNoGo catches go vet leaked into
// a Node/TypeScript phase.
func TestSanitizePhaseVerifyCommandsForStack_nodePhaseNoGo(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "webapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "frontend",
				RequiredFiles:   []string{"webapp/package.json", "webapp/src/app.ts"},
				QAVerifyCommand: "cd webapp && go vet ./...",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := strings.ToLower(got.DeliveryPhases[0].QAVerifyCommand)
	if strings.Contains(cmd, "go vet") || strings.Contains(cmd, "go test") {
		t.Fatalf("node phase kept a Go command: %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
	if !strings.Contains(cmd, "npm") && !strings.Contains(cmd, "tsc") {
		t.Fatalf("node phase should verify with npm/tsc, got %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_pythonPhaseNoNode catches npm leaked into a
// Python phase.
func TestSanitizePhaseVerifyCommandsForStack_pythonPhaseNoNode(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "backend",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "api",
				RequiredFiles:   []string{"backend/main.py"},
				QAVerifyCommand: "cd backend && npm test",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := strings.ToLower(got.DeliveryPhases[0].QAVerifyCommand)
	if strings.Contains(cmd, "npm") {
		t.Fatalf("python phase kept a node command: %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
	if !strings.Contains(cmd, "pytest") && !strings.Contains(cmd, "python") {
		t.Fatalf("python phase should verify with pytest, got %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_keepsMixedStack keeps a command that
// mixes its own stack with another (go test + npm test in a Go+frontend phase).
func TestSanitizePhaseVerifyCommandsForStack_keepsMixedStack(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "full",
				RequiredFiles:   []string{"app/go.mod", "app/frontend/app.ts"},
				QAVerifyCommand: "cd app && go test ./... && cd frontend && npm test",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	if got.DeliveryPhases[0].QAVerifyCommand == "" {
		t.Fatal("mixed-stack command must not be emptied")
	}
	if !strings.Contains(got.DeliveryPhases[0].QAVerifyCommand, "go test") {
		t.Fatalf("mixed-stack Go command must be preserved: %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_configOnlyPhaseNoStackTools confirms a
// config-only phase (requirements.txt) never keeps a Go tool command.
func TestSanitizePhaseVerifyCommandsForStack_configOnlyPhaseNoStackTools(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "config",
				RequiredFiles:   []string{"pingapp/requirements.txt"},
				QAVerifyCommand: "cd pingapp && go vet ./...",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := strings.ToLower(got.DeliveryPhases[0].QAVerifyCommand)
	if strings.Contains(cmd, "go vet") || strings.Contains(cmd, "go test") || strings.Contains(cmd, "go run") {
		t.Fatalf("config-only phase kept a Go command: %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestRebuildDeliveryPhasesFromAuthoritative_repairsDegeneratePhases confirms the
// profile sync collapses the one-phase-per-file skeleton that broke pingapp QA.
func TestRebuildDeliveryPhasesFromAuthoritative_repairsDegeneratePhases(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{ID: "requirementstxt", RequiredFiles: []string{"pingapp/requirements.txt"}},
			{ID: "mainpy", RequiredFiles: []string{"pingapp/main.py", "pingapp/test_main.py", "pingapp/pyproject.toml"}},
			{ID: "test-mainpy", RequiredFiles: []string{}},
		},
	}
	authoritative := []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"}
	got := rebuildDeliveryPhasesFromAuthoritative(v, authoritative)
	if len(got.DeliveryPhases) != 1 {
		t.Fatalf("degenerate skeleton must be rebuilt to 1 phase, got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	if len(got.DeliveryPhases[0].RequiredFiles) != 3 {
		t.Fatalf("rebuilt phase must hold all 3 authoritative files, got %+v", got.DeliveryPhases[0].RequiredFiles)
	}
	// Hallucinated pyproject.toml must be dropped.
	for _, f := range got.DeliveryPhases[0].RequiredFiles {
		if strings.Contains(f, "pyproject.toml") {
			t.Fatalf("hallucinated pyproject.toml survived rebuild: %+v", got.DeliveryPhases[0].RequiredFiles)
		}
	}
}

// TestRebuildDeliveryPhasesFromAuthoritative_handlesLayoutRestructure confirms that
// when the architect restructures a flat layout into subdirectories (main.go →
// cmd/server/main.go), the hollowed-out phase skeleton is rebuilt from the
// authoritative paths instead of dumping every file into the first phase.
func TestRebuildDeliveryPhasesFromAuthoritative_handlesLayoutRestructure(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}},
			{ID: "core", RequiredFiles: []string{"pingapp/main.go"}},
			{ID: "web", RequiredFiles: []string{"pingapp/index.html", "pingapp/app.js"}},
			{ID: "integration-test", RequiredFiles: []string{"pingapp/playwright.config.ts", "pingapp/ping.spec.ts", "pingapp/package.json", "pingapp/docker-compose.yml"}},
		},
	}
	// Architect restructures: flat files move into subdirectories.
	authoritative := []string{
		"pingapp/package.json",
		"pingapp/go.mod",
		"pingapp/cmd/server/main.go",
		"pingapp/web/index.html",
		"pingapp/web/app.js",
		"pingapp/e2e/ping.spec.ts",
		"pingapp/cmd/server/main_test.go",
		"pingapp/playwright.config.ts",
		"pingapp/Dockerfile.web",
		"pingapp/docker-compose.yml",
	}
	got := rebuildDeliveryPhasesFromAuthoritative(v, authoritative)
	if len(got.DeliveryPhases) < 2 {
		t.Fatalf("restructured layout must yield multiple phases, got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	// No single phase may swallow the whole project.
	for _, p := range got.DeliveryPhases {
		if len(p.RequiredFiles) == 0 {
			t.Fatalf("phase %q is empty after rebuild: %+v", p.ID, got.DeliveryPhases)
		}
	}
	// Every authoritative file must be placed exactly once.
	var placed []string
	for _, p := range got.DeliveryPhases {
		placed = append(placed, p.RequiredFiles...)
	}
	if len(placed) != len(authoritative) {
		t.Fatalf("rebuild lost files: got %d placed, want %d (%v)", len(placed), len(authoritative), placed)
	}
	for _, a := range authoritative {
		if !containsString(placed, a) {
			t.Fatalf("authoritative file %q missing after rebuild", a)
		}
	}
	// Semantic phase split must survive the restructure: go.mod stays a go-module
	// file, main.go stays a core file, web files stay in web, docker/playwright
	// files stay in integration-test.
	for _, p := range got.DeliveryPhases {
		switch p.ID {
		case "go-module", "core", "web", "integration-test":
		default:
			t.Fatalf("rebuild produced unexpected phase %q (want SPEC semantic split)", p.ID)
		}
		for _, f := range p.RequiredFiles {
			switch p.ID {
			case "go-module":
				if f != "pingapp/go.mod" {
					t.Fatalf("go-module must hold only go.mod, got %q", f)
				}
			case "core":
				if !strings.Contains(f, "/cmd/") && f != "pingapp/go.mod" {
					t.Fatalf("core must hold cmd/ files, got %q", f)
				}
			case "web":
				if !strings.Contains(f, "/web/") {
					t.Fatalf("web phase must hold web/ files, got %q", f)
				}
			case "integration-test":
				if strings.Contains(f, "/web/") || strings.Contains(f, "/cmd/") {
					t.Fatalf("integration-test must not hold app files, got %q", f)
				}
			}
		}
	}
	// Active phase must resolve after the rebuild, or planning hangs.
	v.ActivePhaseIDField = "go-module"
	got = rebuildDeliveryPhasesFromAuthoritative(v, authoritative)
	if got.ActivePhaseID() == "" || !phaseIDExists(got, got.ActivePhaseID()) {
		t.Fatalf("active_phase_id %q does not resolve after rebuild: %+v", got.ActivePhaseID(), got.DeliveryPhases)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_playwrightComposeClamp is the QA guarantee
// regression: an integration-test phase that ships docker-compose + Playwright must
// always verify with `docker-compose up --exit-code-from playwright`, even when the
// JUDGE rewrote the command to a plain Go test suite.
func TestSanitizePhaseVerifyCommandsForStack_playwrightComposeClamp(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "integration-test",
				RequiredFiles: []string{
					"pingapp/playwright.config.ts",
					"pingapp/e2e/ping.spec.ts",
					"pingapp/package.json",
					"pingapp/Dockerfile",
					"pingapp/docker-compose.yml",
				},
				QAVerifyCommand: "cd pingapp && go build ./... && go test ./...",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := strings.ToLower(got.DeliveryPhases[0].QAVerifyCommand)
	if !strings.Contains(cmd, "up --exit-code-from playwright") {
		t.Fatalf("integration-test phase must verify via playwright compose, got %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
	if !strings.Contains(cmd, "docker-compose") && !strings.Contains(cmd, "docker compose") {
		t.Fatalf("integration-test phase must use the compose CLI, got %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
	if strings.Contains(cmd, "go test") || strings.Contains(cmd, "go build") {
		t.Fatalf("integration-test phase must not run Go tests, got %q", got.DeliveryPhases[0].QAVerifyCommand)
	}
}

// TestSanitizePhaseVerifyCommandsForStack_playwrightComposeSubdirClamp confirms the
// clamp locates a compose file in a subdirectory (test/docker-compose.yml) and passes
// it with -f, so rigs that do not keep compose at the layout root still run Playwright.
func TestSanitizePhaseVerifyCommandsForStack_playwrightComposeSubdirClamp(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "e2e",
				RequiredFiles: []string{
					"pingapp/playwright.config.ts",
					"pingapp/test/docker-compose.yml",
					"pingapp/package.json",
				},
				QAVerifyCommand: "cd pingapp && npx playwright test --list",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := got.DeliveryPhases[0].QAVerifyCommand
	if !strings.Contains(cmd, "-f test/docker-compose.yml down") || !strings.Contains(cmd, "-f test/docker-compose.yml up --exit-code-from playwright") {
		t.Fatalf("subdir compose file must be torn down then brought up via -f, got %q", cmd)
	}
}

// TestClampProfileValidation_playwrightComposeSurvivesLoad is the load-path
// guarantee: the playwright clamp must also fire through ClampProfileValidation,
// which runs on every profile load (task validation, rig sync-planning, rig setup)
// — not just the spec-index post-judge step.
func TestClampProfileValidation_playwrightComposeSurvivesLoad(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "integration-test",
				RequiredFiles: []string{
					"pingapp/playwright.config.ts",
					"pingapp/e2e/ping.spec.ts",
					"pingapp/package.json",
					"pingapp/Dockerfile",
					"pingapp/docker-compose.yml",
				},
				QAVerifyCommand: "cd pingapp && go build ./... && go test ./...",
			},
		},
	}
	got := ClampProfileValidation(v)
	for _, p := range got.DeliveryPhases {
		if p.ID != "integration-test" {
			continue
		}
		cmd := strings.ToLower(p.QAVerifyCommand)
		if !strings.Contains(cmd, "up --exit-code-from playwright") {
			t.Fatalf("ClampProfileValidation must restore the playwright compose command on load, got %q", p.QAVerifyCommand)
		}
	}
}

// TestSanitizePhaseVerifyCommandsForStack_finallyE2ESpecClamp is the FinAlly
// regression: the release phase ships finally/test/e2e.spec.ts + finally/test/
// docker-compose.test.yml (no file path contains the literal "playwright"), and
// the profile's verify was the weak `test -f docker-compose.yml && echo`. The
// clamp must rewrite it to the compose playwright command so the E2E gate
// actually runs.
func TestSanitizePhaseVerifyCommandsForStack_finallyE2ESpecClamp(t *testing.T) {
	prev := dockerComposeCLIOverride
	dockerComposeCLIOverride = "docker-compose"
	t.Cleanup(func() { dockerComposeCLIOverride = prev })

	v := WorkflowValidation{
		LayoutRoot: "finally",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "release",
				Title: "Testing & Release",
				RequiredFiles: []string{
					"finally/Dockerfile",
					"finally/docker-compose.yml",
					"finally/test/docker-compose.test.yml",
					"finally/test/e2e.spec.ts",
					"finally/test/package.json",
				},
				QAVerifyCommand: "cd finally && test -f docker-compose.yml && echo 'compose file ok'",
			},
		},
	}
	got := SanitizePhaseVerifyCommandsForStack(v)
	cmd := got.DeliveryPhases[0].QAVerifyCommand
	if !strings.Contains(cmd, "up --exit-code-from playwright") {
		t.Fatalf("release phase shipping e2e.spec.ts + test compose must verify via playwright compose, got %q", cmd)
	}
	if !strings.Contains(cmd, "-f test/docker-compose.test.yml") {
		t.Fatalf("verify must target the test harness compose file, got %q", cmd)
	}
	if strings.Contains(cmd, "test -f") || strings.Contains(cmd, "echo") {
		t.Fatalf("weak file-existence verify must be rewritten, got %q", cmd)
	}
}
