package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsQATestCommandOK_goTestMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	cmd := "cd testgt3/mayor/rig/linkshelf && go test ./..."
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig go test to match profile QAVerifyCommand")
	}
	if !isImplementationVerifyCommandOK(cmd, "/tmp", "testgt3", "", v) {
		t.Fatal("expected implementation verify to accept mayor/rig go test")
	}
}

func TestIsQATestCommandOK_pythonPytestMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "tasklist",
		TestRunner:      "pytest",
		PythonVenvDir:   ".venv",
		QAVerifyCommand: "cd tasklist && pytest -q",
		RequiredFiles:   []string{"tasklist/app.py"},
	}
	cmd := "cd mockrig/mayor/rig/tasklist && pytest -q"
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig pytest to match profile QAVerifyCommand")
	}
}

func TestIsQATestCommandOK_dockerComposeMayorRigPath(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "finally",
		TestRunner:      "custom",
		QAVerifyCommand: "cd finally && docker-compose -f docker-compose.yml config",
		RequiredFiles:   []string{"finally/docker-compose.yml"},
	}
	cmd := "cd mockrig/mayor/rig/finally && docker-compose -f docker-compose.yml config"
	if !isQATestCommandOK(cmd, v) {
		t.Fatal("expected mayor/rig docker-compose to match profile QAVerifyCommand")
	}
}

func TestDockerVerifyCommandMatches_mayorRigCd(t *testing.T) {
	verify := "cd . && docker-compose -f docker-compose.yml config"
	cmd := "cd finally/mayor/rig && docker-compose -f docker-compose.yml config"
	if !dockerVerifyCommandMatches(cmd, verify) {
		t.Fatal("expected mayor/rig verify to match layout hint")
	}
}
