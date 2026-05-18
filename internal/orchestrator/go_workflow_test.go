package orchestrator

import (
	"strings"
	"testing"
)

func TestWorkflowUsesGo(t *testing.T) {
	t.Parallel()
	if !WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "cd linkshelf && go test ./..."}) {
		t.Fatal("expected Go from qa_verify_command")
	}
	if WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "python3 -m pytest -q"}) {
		t.Fatal("pytest profile should not be Go")
	}
	if !WorkflowUsesGo(WorkflowValidation{TestRunner: "go"}) {
		t.Fatal("test_runner go")
	}
}

func TestGoVerifyCommandWithTidy(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	got := GoVerifyCommandWithTidy(v)
	if !strings.Contains(got, "go mod tidy") || !strings.Contains(got, "go test") {
		t.Fatalf("got %q", got)
	}
}
