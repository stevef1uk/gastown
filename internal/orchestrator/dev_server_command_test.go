package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDevServerCommand_node(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    "app",
		DevServerPort: 3000,
		RequiredFiles: []string{"app/package.json", "app/tests/e2e/trading.spec.ts"},
	}
	if got := DevServerCommand("/rig/mayor/rig/app", v); got != "npm run dev" {
		t.Errorf("node: got %q, want %q", got, "npm run dev")
	}
}

func TestDevServerCommand_pythonUvicorn(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:     ".",
		DevServerPort:  8000,
		TestRunner:     "pytest",
		PythonVenvDir:  ".venv",
		QAVerifyCommand: "cd backend && pytest",
		RequiredFiles:   []string{"backend/main.py", "test/e2e/trading.spec.ts"},
	}
	got := DevServerCommand("/rig/mayor/rig", v)
	if !strings.Contains(got, ".venv/bin/python3 -m uvicorn backend.main:app") {
		t.Errorf("python: got %q, want uvicorn backend.main:app", got)
	}
	if !strings.Contains(got, "--port 8000") {
		t.Errorf("python: expected port 8000, got %q", got)
	}
}

func TestDevServerCommand_pythonNestedLayout(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:     "backend",
		DevServerPort:  8000,
		TestRunner:     "pytest",
		PythonVenvDir:  ".venv",
		QAVerifyCommand: "pytest",
		RequiredFiles:   []string{"backend/main.py", "backend/test/e2e/trading.spec.ts"},
	}
	got := DevServerCommand("/rig/mayor/rig/backend", v)
	if !strings.Contains(got, "../.venv/bin/python3 -m uvicorn main:app") {
		t.Errorf("nested python: got %q, want ../.venv uvicorn main:app", got)
	}
}

func TestDevServerCommand_go(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:     ".",
		DevServerPort:  8080,
		TestRunner:     "go",
		QAVerifyCommand: "cd . && go test ./...",
		RequiredFiles:   []string{"go.mod", "cmd/server/main.go", "e2e/trading.spec.ts"},
	}
	got := DevServerCommand("/rig/mayor/rig", v)
	if got != "go run ./cmd/server" {
		t.Errorf("go: got %q, want %q", got, "go run ./cmd/server")
	}
}

func TestDevServerCommand_goRootMain(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:     ".",
		DevServerPort:  8080,
		TestRunner:     "go",
		QAVerifyCommand: "go test ./...",
		RequiredFiles:   []string{"go.mod", "main.go", "test/e2e/trading.spec.ts"},
	}
	if got := DevServerCommand("/rig/mayor/rig", v); got != "go run ." {
		t.Errorf("go root main: got %q, want %q", got, "go run .")
	}
}

func TestDevServerCommand_noServer(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:    ".",
		DevServerPort: 0, // not a web server
		RequiredFiles: []string{"src/main.ts"},
	}
	if got := DevServerCommand("/rig/mayor/rig", v); got != "" {
		t.Errorf("no server: expected empty command, got %q", got)
	}
}

func TestPythonUvicornModule_prefersBackend(t *testing.T) {
	got := pythonUvicornModule([]string{"backend/main.py", "src/app.py", "test/e2e/a.spec.ts"}, ".")
	if got != "backend.main" {
		t.Errorf("got %q, want backend.main", got)
	}
}

func TestEnsurePlaywrightConfigReady_pythonWebServerCommand(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	writeSpec(t, filepath.Join(rigDir, "test", "e2e", "trading.spec.ts"))

	v := WorkflowValidation{
		LayoutRoot:     ".",
		DevServerPort:  8000,
		TestRunner:     "pytest",
		PythonVenvDir:  ".venv",
		QAVerifyCommand: "cd backend && pytest",
		RequiredFiles:   []string{"backend/main.py", "test/e2e/trading.spec.ts"},
	}

	if _, err := EnsurePlaywrightConfigReady(dir, "myrig", v); err != nil {
		t.Fatalf("EnsurePlaywrightConfigReady failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(rigDir, "playwright.config.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".venv/bin/python3 -m uvicorn backend.main:app") {
		t.Errorf("generated config should run uvicorn backend.main:app, got:\n%s", string(data))
	}
}
