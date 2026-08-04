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
