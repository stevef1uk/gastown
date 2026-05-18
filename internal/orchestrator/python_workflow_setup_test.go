package orchestrator

import (
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

func TestPythonVerifyCommand_layout(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "tasklist", QAVerifyCommand: "pytest -v"}
	got := PythonVerifyCommand(v)
	if !strings.Contains(got, "cd tasklist") || !strings.Contains(got, "pytest") {
		t.Fatalf("got %q", got)
	}
}
