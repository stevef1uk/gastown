package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestIsPythonImportCheckCommand(t *testing.T) {
	t.Parallel()
	cmd := "test -x .venv/bin/python3 && .venv/bin/python3 -c 'import pytest'"
	if !IsPythonImportCheckCommand(cmd) {
		t.Fatal("expected import check")
	}
	if IsPythonImportCheckCommand("cd tasklist && python3 -m pytest -v") {
		t.Fatal("pytest run is not import check")
	}
}

func TestPythonProjectSetupVerifyCommand(t *testing.T) {
	t.Parallel()
	got := PythonProjectSetupVerifyCommand(WorkflowValidation{PythonVenvDir: ".venv"})
	if !strings.Contains(got, "import pytest") || !strings.Contains(got, ".venv/bin/python3") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "pytest -v") {
		t.Fatal("setup verify must not run full pytest")
	}
}

func TestPythonImplementationVerifyCommandForBead_requirements(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "tasklist",
		PythonVenvDir:   ".venv",
		RequiredFiles:   []string{"tasklist/requirements.txt", "tasklist/tasklist/store.py"},
		QAVerifyCommand: "pytest -v",
	}
	got := PythonImplementationVerifyCommandForBead(v, "/tmp", "tasklist/requirements.txt")
	if !strings.Contains(got, "import pytest") || strings.Contains(got, "pytest -v") {
		t.Fatalf("requirements bead should import-check only: %q", got)
	}
	got = PythonImplementationVerifyCommandForBead(v, "/tmp", "tasklist/tasklist/store.py")
	if !strings.Contains(got, "compileall") {
		t.Fatalf("source bead should compileall: %q", got)
	}
	if strings.Contains(got, "cd tasklist") {
		t.Fatalf("venv verify must run from mayor/rig, not cd into layout: %q", got)
	}
	got = PythonImplementationVerifyCommandForBead(v, "/tmp", "tasklist/tasklist/__init__.py")
	if want := ".venv/bin/python3 -m compileall -q tasklist/tasklist/__init__.py"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPythonVenvRelDir_isMayorRigRelative(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "tasklist",
		PythonVenvDir: ".venv",
		RequiredFiles: []string{"tasklist/requirements.txt"},
	}
	if v.PythonVenvRelDir() != ".venv" {
		t.Fatalf("dir: %q", v.PythonVenvRelDir())
	}
	mayorRig := "/tmp/rig/mayor/rig"
	venvPy := filepath.Join(mayorRig, v.PythonVenvRelDir(), "bin", "python3")
	if strings.Contains(venvPy, filepath.Join("tasklist", ".venv")) {
		t.Fatalf("resolved venv must not be under layout: %s", venvPy)
	}
}

func TestPythonVerifyCommand_layout(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "tasklist", QAVerifyCommand: "pytest -v"}
	got := PythonVerifyCommand(v)
	if !strings.Contains(got, "cd tasklist") || !strings.Contains(got, "pytest") {
		t.Fatalf("got %q", got)
	}
}
