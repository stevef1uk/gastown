package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRigFlowQARuntimeSmokeBlock_pythonSkipsGoRun(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:        "backend",
		QAVerifyCommand:   "python3 -m pytest -q",
		RequiredFiles:     []string{"backend/app.py"},
		PythonVenvDir:     ".venv",
	}
	block := RigFlowQARuntimeSmokeBlock(t.TempDir(), "rig", v)
	if strings.Contains(block, "go run ./cmd/server") {
		t.Fatalf("python block must not require go run: %q", block)
	}
	if !strings.Contains(block, "pytest") {
		t.Fatal("want pytest guidance")
	}
}

func TestRigFlowQARuntimeSmokeBlock_goNoAPI(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | / | index |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "app",
		QAVerifyCommand: "cd app && go test ./...",
		RequiredFiles: []string{"app/web/index.html", "app/cmd/server/main.go"},
	}
	block := RigFlowQARuntimeSmokeBlock(dir, rig, v)
	if strings.Contains(block, "/api/") && strings.Contains(block, "POST") {
		t.Fatalf("static-only block should not mandate API POST: %q", block)
	}
	if strings.Contains(block, "POST endpoints") {
		t.Fatalf("static-only block should not mandate API POST: %q", block)
	}
	if !strings.Contains(block, "static") && !strings.Contains(block, "go run") {
		t.Fatalf("want static web smoke guidance: %q", block)
	}
}

func TestRigFlowQARuntimeSmokeBlock_phasedBackendCoreSkipsGoRun(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | /api/links | list |\n| POST | /api/links | create |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		ActivePhaseIDField: "backend-core",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/index.html",
		},
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "backend-core",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/schema.go",
					"linkshelf/internal/store/store.go",
				},
				QAVerifyCommand: "cd linkshelf && go test ./internal/store",
			},
			{
				ID: "server-setup",
				RequiredFiles: []string{
					"linkshelf/cmd/server/main.go",
					"linkshelf/web/index.html",
				},
			},
		},
	}
	block := RigFlowQARuntimeSmokeBlock(dir, rig, v)
	if strings.Contains(block, "go run ./cmd/server") {
		t.Fatalf("backend-core QA block must not require go run: %q", block)
	}
	if !strings.Contains(block, "skip") || !strings.Contains(block, "go test") {
		t.Fatalf("want library-only QA guidance: %q", block)
	}
}

func TestWorkflowNeedsQARuntimeSmoke_pythonWithoutAPI(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -q",
		RequiredFiles:   []string{"backend/fizz.py"},
	}
	if WorkflowNeedsQARuntimeSmoke(t.TempDir(), "rig", v) {
		t.Fatal("python without SPEC API should not require smoke")
	}
}
