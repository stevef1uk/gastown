package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateQACommand_rejectsGoTestOnGoModPhase(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "linkshelf",
		QAVerifyCommand:    "cd linkshelf && go mod download",
		ActivePhaseIDField: "go-module",
		TestRunner:         "custom",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
		},
	}
	v = v.ForActivePhase()
	err := validateQACommand("cd testgt3/mayor/rig && cd linkshelf && go test ./...", "testgt3", "/tmp", v)
	if err == nil {
		t.Fatal("expected go test rejection during go-module QA")
	}
	if !strings.Contains(err.Error(), "go.mod only") {
		t.Fatalf("err = %v", err)
	}
	download := "cd testgt3/mayor/rig && cd linkshelf && go mod download"
	if err := validateQACommand(download, "testgt3", "/tmp", v); err != nil {
		t.Fatalf("download cmd: %v", err)
	}
}

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
