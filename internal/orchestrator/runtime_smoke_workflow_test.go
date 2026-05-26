package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowNeedsRuntimeSmoke_pythonWithAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | /api/items | JSON array when empty |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "backend",
		QAVerifyCommand: "python3 -m pytest -q",
		RequiredFiles:   []string{"backend/app.py"},
		PythonVenvDir:   ".venv",
	}
	if !WorkflowNeedsRuntimeSmoke(dir, rig, v) {
		t.Fatal("expected python HTTP rig to need runtime smoke")
	}
}

func TestDeriveRuntimeSmokeServerStart_python(t *testing.T) {
	t.Parallel()
	docs := "## Runtime smoke server\n.venv/bin/python3 -m uvicorn backend.main:app --host 127.0.0.1 --port 8080\n"
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -q",
		PythonVenvDir:   ".venv",
	}
	got := deriveRuntimeSmokeServerStart(v, docs)
	if !strings.Contains(got, "uvicorn") {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveRuntimeSmokeServerStart_pythonFromQA(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		QAVerifyCommand: "cd backend && .venv/bin/python3 -m uvicorn app:app --port 8080 & python3 -m pytest -q",
		PythonVenvDir:   ".venv",
	}
	got := deriveRuntimeSmokeServerStart(v, "")
	if !strings.Contains(got, "uvicorn app:app") {
		t.Fatalf("got %q", got)
	}
}

func TestIsDevServerSmokeCommand(t *testing.T) {
	t.Parallel()
	if !IsDevServerSmokeCommand("cd app && uvicorn app:app --port 8080") {
		t.Fatal("uvicorn should be dev server smoke")
	}
	if IsDevServerSmokeCommand("python3 -m pytest -q") {
		t.Fatal("pytest alone is not dev server smoke")
	}
}
