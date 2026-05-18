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
	if !WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "cd linkshelf && go run ./cmd/server"}) {
		t.Fatal("expected Go from go run in qa_verify_command")
	}
}

func TestGoProjectSetupVerifyCommand(t *testing.T) {
	t.Parallel()
	got := GoProjectSetupVerifyCommand(WorkflowValidation{LayoutRoot: "linkshelf"})
	if got != "cd linkshelf && go mod tidy" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "go build") || strings.Contains(got, "curl") {
		t.Fatalf("setup verify must not build or curl: %q", got)
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
