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
		LayoutRoot:      "tasklist",
		RequiredFiles:   []string{"tasklist/store.py"},
		QAVerifyCommand: "pytest -v",
	}
	got := PythonImplementationVerifyCommandForBead(v, "/tmp/rig", "tasklist/store.py")
	if !strings.Contains(got, "compileall") || !strings.Contains(got, "tasklist/store.py") {
		t.Fatalf("got %q", got)
	}
}

func TestPythonVerifyCommand_nestedLayoutStripsCD(t *testing.T) {
	t.Parallel()
	// "cd defender/backend && pytest -q" — common for nested layout roots.
	// Should strip the cd prefix and preserve the subdirectory as test scope,
	// because running pytest from inside defender/backend/ breaks imports
	// like "from defender.backend.main import ...".
	v := WorkflowValidation{LayoutRoot: "defender", QAVerifyCommand: "cd defender/backend && pytest -q"}
	got := PythonVerifyCommand(v)
	// Must NOT contain cd defender (would run from wrong directory).
	if strings.Contains(got, "cd defender") {
		t.Fatalf("cd defender should be stripped — pytest runs from mayor/rig: %q", got)
	}
	// Must contain the original scope (defender/backend/) so tests are found.
	if !strings.Contains(got, "defender/backend") {
		t.Fatalf("expected scope defender/backend/ in verify command: %q", got)
	}
	// Must be a pytest command.
	if !strings.Contains(got, "pytest") {
		t.Fatalf("expected pytest in verify: %q", got)
	}
}

func TestPythonVerifyCommand_layoutCDStrippedKeepsScope(t *testing.T) {
	t.Parallel()
	// "cd defender/backend/tests && pytest -v" — deeper nested cd.
	v := WorkflowValidation{LayoutRoot: "defender", QAVerifyCommand: "cd defender/backend/tests && pytest -v"}
	got := PythonVerifyCommand(v)
	if strings.Contains(got, "cd defender") {
		t.Fatalf("cd should be stripped: %q", got)
	}
	if !strings.Contains(got, "defender/backend/tests") {
		t.Fatalf("expected scope preserved: %q", got)
	}
}

func TestPythonImplementationVerifyCommandForBead_envExample(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		RequiredFiles:   []string{"finally/.env.example"},
		QAVerifyCommand: "cd finally && python3 -m pytest backend/tests/test_main.py",
	}
	got := PythonImplementationVerifyCommandForBead(v, "/tmp/rig", ".env.example")
	if got != "test -s .env.example" {
		t.Fatalf("expected generic existence verify for .env.example, got %q", got)
	}
}

func TestPythonVerifyCommand_noCDStillScoped(t *testing.T) {
	t.Parallel()
	// Bare pytest — should get scoped to layout/tests.
	v := WorkflowValidation{LayoutRoot: "defender", QAVerifyCommand: "pytest -v"}
	got := PythonVerifyCommand(v)
	if strings.Contains(got, "cd defender") {
		t.Fatalf("no cd should be added for pytest: %q", got)
	}
	if !strings.Contains(got, "defender/tests") {
		t.Fatalf("expected defender/tests scope: %q", got)
	}
}

func TestPythonVerifyCommand_venvActivationWithLayout(t *testing.T) {
	t.Parallel()
	// Dual-stack rig: qa_verify_command cd's into layout, venv is inside layout.
	// The venv path must be relative to mayor/rig, not the layout cwd.
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		PythonVenvDir:   ".venv",
		QAVerifyCommand: "cd finally && pytest && cd frontend && npm test",
	}
	got := PythonVerifyCommand(v)
	if !strings.Contains(got, ". finally/.venv/bin/activate") {
		t.Fatalf("venv activation must be finally/.venv/bin/activate (relative to mayor/rig): %q", got)
	}
}
