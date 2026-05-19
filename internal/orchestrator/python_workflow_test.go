package orchestrator

import (
	"strings"
	"testing"
)

func TestPythonVerifyCommand_layoutScopedTests(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "tasklist", QAVerifyCommand: "pytest -v"}
	got := PythonVerifyCommand(v)
	if !strings.Contains(got, "tasklist/tests") || !strings.Contains(got, "pytest") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "cd tasklist") {
		t.Fatalf("pytest should run from mayor/rig with scoped path: %q", got)
	}
}

func TestPythonImplementationVerifyCommandForBead_store(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "tasklist",
		RequiredFiles: []string{"tasklist/store.py"},
		QAVerifyCommand: "pytest -v",
	}
	got := PythonImplementationVerifyCommandForBead(v, "/tmp/rig", "tasklist/store.py")
	if !strings.Contains(got, "compileall") || !strings.Contains(got, "tasklist/store.py") {
		t.Fatalf("got %q", got)
	}
}
