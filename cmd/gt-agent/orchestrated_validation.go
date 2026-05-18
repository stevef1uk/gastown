package main

import (
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func taskValidation(task *orchestrator.Task) orchestrator.WorkflowValidation {
	if task == nil {
		return orchestrator.DefaultWorkflowValidation()
	}
	return orchestrator.ClampProfileValidation(task.Validation.WithDefaults())
}

func isProjectSetupVerifyCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if !orchestrator.WorkflowUsesGo(v) {
		return isQATestCommandOK(cmd, v)
	}
	lower := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	if strings.Contains(lower, "go build") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "go test") || strings.Contains(lower, "curl ") {
		return false
	}
	return strings.Contains(lower, "go mod tidy") || strings.Contains(lower, "go mod init") ||
		strings.Contains(lower, "go get ")
}

func isQATestCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if strings.TrimSpace(v.QAVerifyCommand) != "" {
		return commandMatchesQAVerify(cmd, v.QAVerifyCommand)
	}
	return isUnittestCommand(cmd, v.UnittestModule)
}

func isImplementationVerifyCommandOK(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	if isQATestCommandOK(cmd, v) {
		return true
	}
	if !orchestrator.WorkflowUsesGo(v) || rig == "" {
		return false
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	impl := orchestrator.GoImplementationVerifyCommand(v, mayorDir)
	return commandMatchesQAVerify(cmd, impl)
}

func commandMatchesQAVerify(cmd, verify string) bool {
	c := strings.ToLower(strings.TrimSpace(cmd))
	v := strings.ToLower(strings.TrimSpace(verify))
	if v == "" {
		return false
	}
	cNorm := strings.Join(strings.Fields(c), " ")
	vNorm := strings.Join(strings.Fields(v), " ")
	if strings.Contains(cNorm, vNorm) {
		return true
	}
	return strings.Contains(c, v)
}
