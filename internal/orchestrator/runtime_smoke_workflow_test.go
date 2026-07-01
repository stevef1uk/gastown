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

func TestPythonTestsAllPassed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"collected 3 items\ntest_main.py ...\n1 passed, 2 warnings", true},
		{"collected 3 items\ntest_main.py ...\n3 passed", true},
		{"collected 1 item\ntest_main.py F\n1 failed", false},
		{"collected 0 items", false},
		{"ModuleNotFoundError: No module named 'fastapi'", false},
		{"ERROR collecting test_main.py\nimport error", false},
	}
	for _, c := range cases {
		got := PythonTestsAllPassed(c.output)
		if got != c.want {
			t.Fatalf("PythonTestsAllPassed(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestPythonVerifyNoTestsOK(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"no tests ran", true},
		{"collected 0 items", true},
		{"collected 3 items\ntest_main.py ...\n3 passed", false},
		{"collected 1 item\ntest_main.py F\n1 failed", false},
	}
	for _, c := range cases {
		got := PythonVerifyNoTestsOK(c.output)
		if got != c.want {
			t.Fatalf("PythonVerifyNoTestsOK(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestIsDevServerSmokeCommand(t *testing.T) {
	t.Parallel()

	// Should match: real server starts
	cases := []string{
		"cd app && uvicorn app:app --port 8080",
		".venv/bin/python -m uvicorn pingapp.main:app --host 127.0.0.1",
		"cd rig && .venv/bin/python3 -m hypercorn app:app",
		"gunicorn myapp:app",
		"flask run",
	}
	for _, c := range cases {
		if !IsDevServerSmokeCommand(c) {
			t.Fatalf("should match as dev server smoke: %q", c)
		}
	}

	// Should NOT match: pip install, python -c imports, pytest
	noCases := []string{
		"python3 -m pytest -q",
		"pip install uvicorn fastapi pytest",
		".venv/bin/pip3 install --quiet fastapi uvicorn pytest httpx",
		`python3 -c "import fastapi,uvicorn,httpx,pytest;print(fastapi.__version__)"`,
	}
	for _, c := range noCases {
		if IsDevServerSmokeCommand(c) {
			t.Fatalf("should NOT match as dev server smoke: %q", c)
		}
	}
}
