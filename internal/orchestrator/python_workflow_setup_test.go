package orchestrator

import (
	"strings"
	"testing"
)

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
