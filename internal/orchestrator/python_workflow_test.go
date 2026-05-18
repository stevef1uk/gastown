package orchestrator

import "testing"

func TestWorkflowUsesPython(t *testing.T) {
	t.Parallel()
	if !WorkflowUsesPython(WorkflowValidation{QAVerifyCommand: "cd backend && python3 -m pytest -q"}) {
		t.Fatal("expected Python from pytest verify")
	}
	if WorkflowUsesPython(WorkflowValidation{QAVerifyCommand: "cd linkshelf && go test ./..."}) {
		t.Fatal("Go profile should not be Python")
	}
	if !WorkflowUsesPython(WorkflowValidation{RequiredFiles: []string{"backend/requirements.txt"}}) {
		t.Fatal("requirements.txt implies Python venv")
	}
}

func TestUsesPythonVenv_off(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{PythonVenvDir: "off", RequiredFiles: []string{"backend/requirements.txt"}}
	if v.UsesPythonVenv() {
		t.Fatal("python_venv_dir off should disable")
	}
}
